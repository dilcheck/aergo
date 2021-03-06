// Copyright 2015 The etcd Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package raftv2

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"github.com/aergoio/aergo/chain"
	"github.com/aergoio/aergo/p2p/p2pcommon"
	"github.com/aergoio/aergo/pkg/component"
	"github.com/gogo/protobuf/proto"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime/debug"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aergoio/aergo/consensus"
	"github.com/aergoio/aergo/types"

	"github.com/aergoio/etcd/etcdserver/stats"
	etcdtypes "github.com/aergoio/etcd/pkg/types"
	raftlib "github.com/aergoio/etcd/raft"
	"github.com/aergoio/etcd/raft/raftpb"
	"github.com/aergoio/etcd/rafthttp"
	"github.com/aergoio/etcd/snap"
)

//noinspection ALL
var (
	raftLogger                  raftlib.Logger
	ConfSnapFrequency           uint64 = 10
	ConfSnapshotCatchUpEntriesN uint64 = ConfSnapFrequency
)

var (
	ErrNoSnapshot          = errors.New("no snapshot")
	ErrCCAlreadyApplied    = errors.New("conf change entry is already applied")
	ErrInvalidMember       = errors.New("member of conf change is invalid")
	ErrCCAlreadyAdded      = errors.New("member has already added")
	ErrCCNoMemberToRemove  = errors.New("there is no member to remove")
	ErrEmptySnapshot       = errors.New("received empty snapshot")
	ErrInvalidRaftIdentity = errors.New("raft identity is not set")
)

const (
	HasNoLeader uint64 = 0
)

func init() {
	raftLogger = NewRaftLogger(logger)
}

// A key-value stream backed by raft
// A key-value stream backed by raft
type raftServer struct {
	*component.ComponentHub

	pa p2pcommon.PeerAccessor

	cluster *Cluster

	confChangeC <-chan *consensus.ConfChangePropose // proposed cluster config changes
	commitC     chan *types.Block                   // entries committed to log (k,v)
	errorC      chan error                          // errors from raft session

	id          uint64 // client ID for raft session
	listenUrl   string
	join        bool // node is joining an existing cluster
	getSnapshot func() ([]byte, error)
	lastIndex   uint64 // index of log at start

	snapshotIndex uint64
	appliedIndex  uint64

	// raft backing for the commit/error channel
	node        raftlib.Node
	raftStorage *raftlib.MemoryStorage
	//wal         *wal.WAL
	walDB *WalDB

	snapshotter      *ChainSnapshotter
	snapshotterReady chan *snap.Snapshotter // signals when snapshotter is ready

	snapFrequency uint64
	transport     *rafthttp.Transport
	stopc         chan struct{} // signals proposal channel closed
	httpstopc     chan struct{} // signals http server to shutdown
	httpdonec     chan struct{} // signals http server shutdown complete

	leaderStatus LeaderStatus

	certFile string
	keyFile  string

	lock       sync.RWMutex
	promotable bool

	tickMS time.Duration

	confState    raftpb.ConfState
	progress     BlockProgress
	prevProgress BlockProgress // prev state before appling last block
}

type BlockProgress struct {
	block     *types.Block //tracking last applied block. It's initillay set at repling wal
	index     uint64
	term      uint64
	confState raftpb.ConfState
}

func (prog *BlockProgress) isEmpty() bool {
	return prog.block == nil
}

type LeaderStatus struct {
	leader        uint64
	leaderChanged uint64
}

func RecoverExit() {
	if r := recover(); r != nil {
		logger.Error().Str("callstack", string(debug.Stack())).Msg("panic occurred in raft server")
		os.Exit(10)
	}
}

func makeConfig(nodeID uint64, storage *raftlib.MemoryStorage) *raftlib.Config {
	c := &raftlib.Config{
		ID:                        nodeID,
		ElectionTick:              10,
		HeartbeatTick:             1,
		Storage:                   storage,
		MaxSizePerMsg:             1024 * 1024,
		MaxInflightMsgs:           256,
		Logger:                    raftLogger,
		CheckQuorum:               true,
		DisableProposalForwarding: true,
	}

	return c
}

// newRaftServer initiates a raft instance and returns a committed log entry
// channel and error channel. Proposals for log updates are sent over the
// provided the proposal channel. All log entries are replayed over the
// commit channel, followed by a nil message (to indicate the channel is
// current), then new log entries. To shutdown, close proposeC and read errorC.
func newRaftServer(hub *component.ComponentHub,
	cluster *Cluster,
	listenUrl string, join bool,
	certFile string, keyFile string,
	getSnapshot func() ([]byte, error),
	tickMS time.Duration,
	confChangeC chan *consensus.ConfChangePropose,
	commitC chan *types.Block,
	delayPromote bool,
	chainWal consensus.ChainWAL) *raftServer {

	errorC := make(chan error, 1)

	rs := &raftServer{
		ComponentHub:  hub,
		cluster:       cluster,
		walDB:         NewWalDB(chainWal),
		confChangeC:   confChangeC,
		commitC:       commitC,
		errorC:        errorC,
		listenUrl:     listenUrl,
		join:          join,
		getSnapshot:   getSnapshot,
		snapFrequency: ConfSnapFrequency,
		stopc:         make(chan struct{}),
		httpstopc:     make(chan struct{}),
		httpdonec:     make(chan struct{}),

		snapshotterReady: make(chan *snap.Snapshotter, 1),
		// rest of structure populated after WAL replay

		certFile: certFile,
		keyFile:  keyFile,

		lock:       sync.RWMutex{},
		promotable: true,
		tickMS:     tickMS,
	}

	if delayPromote {
		rs.SetPromotable(false)
	}

	rs.snapshotter = newChainSnapshotter(nil, rs.ComponentHub, rs.cluster, rs.walDB, func() uint64 { return rs.GetLeader() })

	return rs
}

func (rs *raftServer) SetID(id uint64) {
	rs.id = id
}

func (rs *raftServer) SetPeerAccessor(pa p2pcommon.PeerAccessor) {
	rs.pa = pa
	rs.snapshotter.setPeerAccessor(pa)
}

func (rs *raftServer) SetPromotable(val bool) {
	defer rs.lock.Unlock()
	rs.lock.Lock()
	rs.promotable = val
}

func (rs *raftServer) GetPromotable() bool {
	defer rs.lock.RUnlock()

	rs.lock.RLock()
	val := rs.promotable

	return val
}

func (rs *raftServer) Start() {
	go rs.startRaft()
}

func (rs *raftServer) makeStartPeers() ([]raftlib.Peer, error) {
	return rs.cluster.getStartPeers()
}

type RaftServerState int

const (
	RaftServerStateRestart = iota
	RaftServerStateNewCluster
	RaftServerStateJoinCluster
)

func (rs *raftServer) startRaft() {
	var node raftlib.Node

	getState := func() RaftServerState {
		hasWal, err := rs.walDB.HasWal()
		if err != nil {
			logger.Fatal().Msg("failed to check if wal has initializeded")
		}

		switch {
		case hasWal:
			return RaftServerStateRestart
		case rs.join:
			return RaftServerStateJoinCluster
		default:
			return RaftServerStateNewCluster
		}

	}

	switch getState() {
	case RaftServerStateRestart:
		logger.Info().Msg("raft restart from wal")

		rs.cluster.ResetMembers()

		snapshot, err := rs.loadSnapshot()
		if err != nil {
			logger.Fatal().Err(err).Msg("failed to read snapshot")
		}

		if err := rs.replayWAL(snapshot); err != nil {
			logger.Fatal().Err(err).Msg("replay wal failed for raft")
		}

		// cluster identity is recoverd from wal
		rs.SetID(rs.cluster.NodeID())

		// members of cluster will be loaded from snapshot or wal
		if snapshot != nil {
			if err := rs.cluster.Recover(snapshot); err != nil {
				logger.Fatal().Err(err).Msg("failt to recover cluster from snapshot")
			}
		}

		c := makeConfig(rs.id, rs.raftStorage)

		logger.Info().Msg("raft node restart")
		node = raftlib.RestartNode(c)
	case RaftServerStateJoinCluster:
		logger.Info().Msg("raft start at first time and join existing cluster")

		// get cluster info from existing cluster member
		existCluster, err := rs.GetExistingCluster()
		if err != nil {
			logger.Fatal().Err(err).Str("mine", rs.cluster.toString()).Msg("failed to get existing cluster info")
		}

		// config validate
		if !rs.cluster.ValidateAndMergeExistingCluster(existCluster) {
			logger.Fatal().Str("existcluster", existCluster.toString()).Str("mycluster", rs.cluster.toString()).Msg("this cluster configuration is not compatible with existing cluster")
		}

		rs.SetID(rs.cluster.NodeID())
		if err := rs.SaveIdentity(); err != nil {
			logger.Fatal().Err(err).Msg("fafiled to save identity")
		}

		rs.raftStorage = raftlib.NewMemoryStorage()

		// reset my raft nodeID from existing cluster
		c := makeConfig(rs.id, rs.raftStorage)

		logger.Info().Msg("raft node start")
		node = raftlib.StartNode(c, nil)
	case RaftServerStateNewCluster:
		logger.Info().Msg("raft start at first time and makes new cluster")

		var startPeers []raftlib.Peer

		if err := rs.cluster.SetThisNodeID(); err != nil {
			logger.Fatal().Err(err).Msg("failed to set id of this node")
		}
		rs.SetID(rs.cluster.NodeID())
		if err := rs.SaveIdentity(); err != nil {
			logger.Fatal().Err(err).Msg("fafiled to save identity")
		}

		startPeers, err := rs.makeStartPeers()
		if err != nil {
			logger.Fatal().Err(err).Msg("failed to make raft peer list")
		}

		rs.raftStorage = raftlib.NewMemoryStorage()

		c := makeConfig(rs.id, rs.raftStorage)

		logger.Info().Msg("raft node start")
		node = raftlib.StartNode(c, startPeers)
	}

	// need locking for sync with consensusAccessor
	rs.setNodeSync(node)

	rs.startTransport()

	go rs.serveRaft()
	go rs.serveChannels()
}

func (rs *raftServer) startTransport() {
	rs.transport = &rafthttp.Transport{
		ID:          etcdtypes.ID(rs.id),
		ClusterID:   0x1000,
		Raft:        rs,
		ServerStats: stats.NewServerStats("", ""),
		LeaderStats: stats.NewLeaderStats(strconv.FormatUint(uint64(rs.id), 10)),
		Snapshotter: rs.snapshotter,
		ErrorC:      rs.errorC,
	}

	rs.transport.SetLogger(httpLogger)

	if err := rs.transport.Start(); err != nil {
		logger.Fatal().Err(err).Msg("failed to start raft http")
	}

	for _, member := range rs.cluster.getMembers().MapByID {
		if rs.cluster.NodeID() != member.ID {
			rs.transport.AddPeer(etcdtypes.ID(member.ID), []string{member.Url})
		}
	}
}

func (rs *raftServer) SaveIdentity() error {
	if rs.cluster.NodeID() == consensus.InvalidMemberID || len(rs.cluster.NodeName()) == 0 {
		return ErrInvalidRaftIdentity
	}

	if err := rs.walDB.WriteIdentity(&rs.cluster.identity); err != nil {
		logger.Fatal().Err(err).Msg("failed to write raft identity to wal")
		return err
	}

	return nil
}

func (rs *raftServer) setNodeSync(node raftlib.Node) {
	defer rs.lock.Unlock()

	rs.lock.Lock()
	rs.node = node
}

func (rs *raftServer) getNodeSync() raftlib.Node {
	defer rs.lock.RUnlock()

	var node raftlib.Node
	rs.lock.RLock()
	node = rs.node

	return node
}

// stop closes http, closes all channels, and stops raft.
func (rs *raftServer) stop() {
	rs.stopHTTP()
	close(rs.commitC)
	close(rs.errorC)
	rs.node.Stop()
}

func (rs *raftServer) stopHTTP() {
	rs.transport.Stop()
	close(rs.httpstopc)
	<-rs.httpdonec
}

func (rs *raftServer) writeError(err error) {
	logger.Error().Err(err).Msg("write err has occurend raft server. ")
}

// TODO timeout handling with context
func (rs *raftServer) Propose(block *types.Block) error {
	if data, err := marshalEntryData(block); err == nil {
		// blocks until accepted by raft state machine
		if err := rs.node.Propose(context.TODO(), data); err != nil {
			return err
		}

		logger.Debug().Int("len", len(data)).Msg("proposed data to raft node")
	} else {
		logger.Fatal().Err(err).Msg("poposed data is invalid")
	}

	return nil
}

func (rs *raftServer) serveConfChange() {
	handleConfChange := func(propose *consensus.ConfChangePropose) {
		if err := rs.node.ProposeConfChange(context.TODO(), *propose.Cc); err != nil {
			logger.Error().Err(err).Msg("failed to propose configure change")
			rs.cluster.sendConfChangeReply(propose.Cc, nil, err)
			return
		}
	}

	// send proposals over raft
	for rs.confChangeC != nil {
		select {
		case confChangePropose, ok := <-rs.confChangeC:
			if !ok {
				rs.confChangeC = nil
			} else {
				handleConfChange(confChangePropose)
			}
		}
	}
	// client closed channel; shutdown raft if not already
	close(rs.stopc)
}

func (rs *raftServer) serveChannels() {
	defer RecoverExit()

	snapshot, err := rs.raftStorage.Snapshot()
	if err != nil {
		panic(err)
	}
	rs.setConfState(snapshot.Metadata.ConfState)
	rs.setSnapshotIndex(snapshot.Metadata.Index)
	rs.setAppliedIndex(snapshot.Metadata.Index)

	ticker := time.NewTicker(rs.tickMS)
	defer ticker.Stop()

	go rs.serveConfChange()

	// event loop on raft state machine updates
	for {
		select {
		case <-ticker.C:
			if rs.GetPromotable() {
				rs.node.Tick()
			}

			// store raft entries to walDB, then publish over commit channel
		case rd := <-rs.node.Ready():
			if len(rd.Entries) > 0 || len(rd.CommittedEntries) > 0 || !raftlib.IsEmptyHardState(rd.HardState) {
				logger.Debug().Int("entries", len(rd.Entries)).Int("commitentries", len(rd.CommittedEntries)).Str("hardstate", rd.HardState.String()).Msg("ready to process")
			}

			if rs.IsLeader() {
				if err := rs.processMessages(rd.Messages); err != nil {
					logger.Fatal().Err(err).Msg("leader process message error")
				}
			}

			if err := rs.walDB.SaveEntry(rd.HardState, rd.Entries); err != nil {
				logger.Fatal().Err(err).Msg("failed to save entry to wal")
			}

			if !raftlib.IsEmptySnap(rd.Snapshot) {
				if err := rs.walDB.WriteSnapshot(&rd.Snapshot); err != nil {
					logger.Fatal().Err(err).Msg("failed to save snapshot to wal")
				}

				if err := rs.raftStorage.ApplySnapshot(rd.Snapshot); err != nil {
					logger.Fatal().Err(err).Msg("failed to apply snapshot")
				}

				if err := rs.publishSnapshot(rd.Snapshot); err != nil {
					logger.Fatal().Err(err).Msg("failed to publish snapshot")
				}
			}
			if err := rs.raftStorage.Append(rd.Entries); err != nil {
				logger.Fatal().Err(err).Msg("failed to append new entries to raft log")
			}

			if !rs.IsLeader() {
				if err := rs.processMessages(rd.Messages); err != nil {
					logger.Fatal().Err(err).Msg("process message error")
				}
			}
			if ok := rs.publishEntries(rs.entriesToApply(rd.CommittedEntries)); !ok {
				rs.stop()
				return
			}
			rs.triggerSnapshot()

			// New block must be created after connecting all commited block
			if rd.SoftState != nil {
				rs.updateLeader(rd.SoftState)
			}

			rs.node.Advance()
		case err := <-rs.errorC:
			rs.writeError(err)
			return

		case <-rs.stopc:
			rs.stop()
			return
		}
	}
}

func (rs *raftServer) processMessages(msgs []raftpb.Message) error {
	var err error
	var tmpSnapMsg *snap.Message

	snapMsgs := make([]*snap.Message, 0)

	// reset MsgSnap to send snap.Message
	for i, msg := range msgs {
		if msg.Type == raftpb.MsgSnap {
			tmpSnapMsg, err = rs.makeSnapMessage(&msg)
			if err != nil {
				return err
			}
			snapMsgs = append(snapMsgs, tmpSnapMsg)

			msgs[i].To = 0
		}
	}

	rs.transport.Send(msgs)

	for _, tmpSnapMsg := range snapMsgs {
		rs.transport.SendSnapshot(*tmpSnapMsg)
	}

	return nil
}

func (rs *raftServer) makeSnapMessage(msg *raftpb.Message) (*snap.Message, error) {
	if msg.Type != raftpb.MsgSnap {
		return nil, ErrNotMsgSnap
	}

	/*
		// make snapshot with last progress of raftserver
		snapshot, err := rs.snapshotter.createSnapshot(rs.prevProgress, rs.confState)
		if err != nil {
			return nil, err
		}

		msg.Snapshot = *snapshot
	*/
	// TODO add cluster info to snapshot.data

	logger.Debug().Uint64("term", msg.Term).Uint64("index", msg.Index).Msg("send merged snapshot message")

	// not using pipe to send snapshot
	pr, pw := io.Pipe()

	go func() {
		buf := new(bytes.Buffer)
		err := binary.Write(buf, binary.LittleEndian, int32(1))
		if err != nil {
			logger.Fatal().Err(err).Msg("raft pipe binary write err")
		}

		n, err := pw.Write(buf.Bytes())
		if err == nil {
			logger.Debug().Msgf("wrote database snapshot out [total bytes: %d]", n)
		} else {
			logger.Error().Msgf("failed to write database snapshot out [written bytes: %d]: %v", n, err)
		}
		if err := pw.CloseWithError(err); err != nil {
			logger.Fatal().Err(err).Msg("raft pipe close error")
		}
	}()

	return snap.NewMessage(*msg, pr, 4), nil
}

func (rs *raftServer) serveRaft() {
	defer RecoverExit()

	urlstr := rs.listenUrl
	urlData, err := url.Parse(urlstr)
	if err != nil {
		logger.Fatal().Err(err).Str("url", urlstr).Msg("Failed parsing URL")
	}

	ln, err := newStoppableListener(urlData.Host, rs.httpstopc)
	if err != nil {
		logger.Fatal().Err(err).Str("url", urlstr).Msg("Failed to listen rafthttp")
	}

	if len(rs.certFile) != 0 && len(rs.keyFile) != 0 {
		logger.Info().Str("url", urlstr).Str("certfile", rs.certFile).Str("keyfile", rs.keyFile).
			Msg("raft http server(tls) started")

		err = (&http.Server{Handler: rs.transport.Handler()}).ServeTLS(ln, rs.certFile, rs.keyFile)
	} else {
		logger.Info().Str("url", urlstr).Msg("raft http server started")

		err = (&http.Server{Handler: rs.transport.Handler()}).Serve(ln)
	}

	select {
	case <-rs.httpstopc:
	default:
		logger.Fatal().Err(err).Msg("Failed to serve rafthttp")
	}
	close(rs.httpdonec)
}

func (rs *raftServer) loadSnapshot() (*raftpb.Snapshot, error) {
	snapshot, err := rs.walDB.GetSnapshot()
	if err != nil {
		logger.Fatal().Err(err).Msg("error loading snapshot")
		return nil, err
	}

	if snapshot == nil {
		logger.Info().Msg("snapshot does not exist")
		return nil, nil
	}

	snapdata := &consensus.SnapshotData{}

	err = snapdata.Decode(snapshot.Data)
	if err != nil {
		logger.Fatal().Err(err).Msg("error decoding snapshot")
		return nil, err
	}

	logger.Info().Str("meta", consensus.SnapToString(snapshot, snapdata)).Msg("loaded snapshot meta")

	return snapshot, nil
}

/*
// openWAL returns a WAL ready for reading.
func (rs *raftServer) openWAL(snapshot *raftpb.Snapshot) *wal.WAL {
	if !wal.Exist(rs.waldir) {
		if err := os.MkdirAll(rs.waldir, 0750); err != nil {
			logger.Fatal().Err(err).Msg("cannot create dir for walDB")
		}

		w, err := wal.Create(rs.waldir, nil)
		if err != nil {
			logger.Fatal().Err(err).Msg("create walDB error")
		}

		logger.Info().Str("dir", rs.waldir).Msg("create walDB directory")
		w.Close()
	}

	walsnap := walpb.Snapshot{}
	if snapshot != nil {
		walsnap.Index, walsnap.Term = snapshot.Metadata.Index, snapshot.Metadata.Term
	}
	logger.Info().Uint64("term", walsnap.Term).Uint64("index", walsnap.Index).Msg("loading WAL at term %d and index")
	w, err := wal.Open(rs.waldir, walsnap)
	if err != nil {
		logger.Fatal().Err(err).Msg("error loading walDB")
	}

	logger.Info().Msg("openwal done")
	return w
}
*/
func (rs *raftServer) updateBlockProgress(term uint64, index uint64, block *types.Block) {
	if block == nil {
		return
	}

	logger.Debug().Uint64("term", term).Uint64("index", index).Uint64("no", block.BlockNo()).Str("hash", block.ID()).Msg("set progress of last block")

	rs.prevProgress = rs.progress

	rs.progress.term = term
	rs.progress.index = index
	rs.progress.block = block
	rs.progress.confState = rs.confState
}

// replayWAL replays WAL entries into the raft instance.
func (rs *raftServer) replayWAL(snapshot *raftpb.Snapshot) error {
	logger.Info().Msg("replaying WAL")

	identity, st, ents, err := rs.walDB.ReadAll(snapshot)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to read WAL")
		return err
	}

	if err := rs.cluster.RecoverIdentity(identity); err != nil {
		logger.Fatal().Err(err).Msg("failed to recover raft identity from wal")
	}

	rs.raftStorage = raftlib.NewMemoryStorage()
	if snapshot != nil {
		if err := rs.raftStorage.ApplySnapshot(*snapshot); err != nil {
			logger.Fatal().Err(err).Msg("failed to apply snapshot to reaply wal")
		}
	}
	if err := rs.raftStorage.SetHardState(*st); err != nil {
		logger.Fatal().Err(err).Msg("failed to set hard state to reaply wal")
	}

	// append to storage so raft starts at the right place in log
	if err := rs.raftStorage.Append(ents); err != nil {
		logger.Fatal().Err(err).Msg("failed to append entries to reaply wal")
	}
	// send nil once lastIndex is published so client knows commit channel is current
	if len(ents) > 0 {
		rs.lastIndex = ents[len(ents)-1].Index
	}

	logger.Info().Uint64("lastindex", rs.lastIndex).Msg("replaying WAL done")

	return nil
}

/*
// createSnapshot make marshalled data of chain & cluster info
func (rs *raftServer) createSnapshot() ([]byte, error) {
	// this snapshot is used when reboot and initialize raft log
	if rs.prevProgress.isEmpty() {
		logger.Fatal().Msg("last applied block is nil")
	}

	snapBlock := rs.prevProgress.block

	logger.Info().Str("hash", snapBlock.ID()).Uint64("no", snapBlock.BlockNo()).Msg("create new snapshot of chain")

	snap := consensus.NewChainSnapshot(snapBlock)
	if snap == nil {
		panic("new snap failed")
	}

	return snap.Encode()
}*/

// triggerSnapshot create snapshot and make compaction for raft log storage
// raft can not wait until last applied entry commits. so snapshot must create from rs.prevProgress.index
func (rs *raftServer) triggerSnapshot() {
	if rs.prevProgress.index == 0 || rs.prevProgress.block == nil {
		return
	}

	newSnapshotIndex := rs.prevProgress.index

	if newSnapshotIndex-rs.snapshotIndex <= rs.snapFrequency {
		return
	}

	logger.Info().Uint64("applied", rs.appliedIndex).Uint64("new snap index", newSnapshotIndex).Uint64("last snapshot index", rs.snapshotIndex).Msg("start snapshot")

	// make snapshot data of previous connected block
	snapdata, err := rs.snapshotter.createSnapshotData(rs.cluster, rs.prevProgress.block, &rs.confState)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to create snapshot data from prev block")
	}

	data, err := snapdata.Encode()
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to marshal snapshot data")
	}

	// snapshot.data is not used for snapshot transfer. At the time of transmission, a message is generated again with information at that time and sent.
	snapshot, err := rs.raftStorage.CreateSnapshot(newSnapshotIndex, &rs.prevProgress.confState, data)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to create snapshot")
	}

	// save snapshot to wal
	if err := rs.walDB.WriteSnapshot(&snapshot); err != nil {
		logger.Fatal().Err(err).Msg("failed to write snapshot")
	}

	compactIndex := uint64(1)
	if newSnapshotIndex > ConfSnapshotCatchUpEntriesN {
		compactIndex = newSnapshotIndex - ConfSnapshotCatchUpEntriesN
	}
	if err := rs.raftStorage.Compact(compactIndex); err != nil {
		if err == raftlib.ErrCompacted {
			return
		}
		panic(err)
	}

	logger.Info().Uint64("index", compactIndex).Msg("compacted raftLog.at index")
	rs.setSnapshotIndex(newSnapshotIndex)

	chain.TestDebugger.Check(chain.DEBUG_RAFT_SNAP_FREQ, 0,
		func(freq int) error {
			rs.snapFrequency = uint64(freq)
			return nil
		})
}

func (rs *raftServer) publishSnapshot(snapshotToSave raftpb.Snapshot) error {
	if raftlib.IsEmptySnap(snapshotToSave) {
		return ErrEmptySnapshot
	}

	logger.Info().Uint64("index", rs.snapshotIndex).Str("snap", consensus.SnapToString(&snapshotToSave, nil)).Msg("publishing snapshot at index")
	defer logger.Info().Uint64("index", rs.snapshotIndex).Msg("finished publishing snapshot at index")

	if snapshotToSave.Metadata.Index <= rs.appliedIndex {
		logger.Fatal().Msgf("snapshot index [%d] should > progress.appliedIndex [%d] + 1", snapshotToSave.Metadata.Index, rs.appliedIndex)
	}
	//rs.commitC <- nil // trigger kvstore to load snapshot

	rs.setConfState(snapshotToSave.Metadata.ConfState)
	rs.setSnapshotIndex(snapshotToSave.Metadata.Index)
	rs.setAppliedIndex(snapshotToSave.Metadata.Index)

	rs.prevProgress.index = 0
	rs.progress.index = 0

	if err := rs.cluster.Recover(&snapshotToSave); err != nil {
		return err
	}

	rs.recoverTransport()

	return nil
}

func (rs *raftServer) recoverTransport() {
	logger.Info().Msg("remove all peers")
	rs.transport.RemoveAllPeers()

	for _, m := range rs.cluster.members.MapByID {
		if m.ID == rs.cluster.NodeID() {
			continue
		}

		logger.Info().Str("member", m.ToString()).Msg("add raft peer")
		rs.transport.AddPeer(etcdtypes.ID(uint64(m.ID)), []string{m.Url})
	}
}

func (rs *raftServer) entriesToApply(ents []raftpb.Entry) (nents []raftpb.Entry) {
	if len(ents) == 0 {
		return
	}
	firstIdx := ents[0].Index
	if firstIdx > rs.appliedIndex+1 {
		logger.Fatal().Msgf("first index of committed entry[%d] should <= progress.appliedIndex[%d] 1", firstIdx, rs.appliedIndex)
	}
	if rs.appliedIndex-firstIdx+1 < uint64(len(ents)) {
		nents = ents[rs.appliedIndex-firstIdx+1:]
	}
	return nents
}

var (
	ErrInvCCType = errors.New("change type of ")
)

func (rs *raftServer) ValidateConfChangeEntry(entry *raftpb.Entry) (*raftpb.ConfChange, *consensus.Member, error) {
	// TODO XXX validate from current cluster configure
	var cc *raftpb.ConfChange
	var member *consensus.Member
	var err error

	alreadyApplied := func(entry *raftpb.Entry) bool {
		return rs.cluster.appliedTerm >= entry.Term || rs.cluster.appliedIndex >= entry.Index
	}

	if alreadyApplied(entry) {
		return nil, nil, ErrCCAlreadyApplied
	}

	unmarshalConfChangeEntry := func() (*raftpb.ConfChange, *consensus.Member, error) {
		var cc raftpb.ConfChange

		if err := cc.Unmarshal(entry.Data); err != nil {
			logger.Fatal().Err(err).Uint64("idx", entry.Index).Uint64("term", entry.Term).Msg("failed to unmarshal of conf change entry")
			return nil, nil, err
		}

		// skip confChange of empty context
		if len(cc.Context) == 0 {
			return nil, nil, nil
		}

		var member = consensus.Member{}
		if err := json.Unmarshal(cc.Context, &member); err != nil {
			logger.Fatal().Err(err).Uint64("idx", entry.Index).Uint64("term", entry.Term).Msg("failed to unmarshal of context of cc entry")
			return nil, nil, err
		}

		return &cc, &member, nil
	}

	cc, member, err = unmarshalConfChangeEntry()

	if err = rs.cluster.validateChangeMembership(cc, member, true); err != nil {
		return cc, member, err
	}

	return cc, member, nil
}

// TODO refactoring by cc.Type
//      separate unmarshal & apply[type]
// applyConfChange returns false if this node is removed from cluster
func (rs *raftServer) applyConfChange(ent *raftpb.Entry) bool {
	var cc *raftpb.ConfChange
	var member *consensus.Member
	var err error

	if cc, member, err = rs.ValidateConfChangeEntry(ent); err != nil {
		logger.Warn().Err(err).Str("cluster", rs.cluster.toString()).Msg("failed to validate conf change")
		// reset pending conf change
		cc.NodeID = raftlib.None
		rs.node.ApplyConfChange(*cc)

		return true
	}

	rs.confState = *rs.node.ApplyConfChange(*cc)

	logger.Info().Str("type", cc.Type.String()).Str("member", member.ToString()).Msg("publish confChange entry")

	switch cc.Type {
	case raftpb.ConfChangeAddNode:
		if err := rs.cluster.addMember(member, false); err != nil {
			logger.Fatal().Str("member", member.ToString()).Msg("failed to add member to cluster")
		}

		if len(cc.Context) > 0 && rs.id != cc.NodeID {
			rs.transport.AddPeer(etcdtypes.ID(cc.NodeID), []string{member.Url})
		} else {
			logger.Debug().Msg("skip add peer myself for addnode ")
		}
	case raftpb.ConfChangeRemoveNode:
		if err := rs.cluster.removeMember(member); err != nil {
			logger.Fatal().Str("member", member.ToString()).Msg("failed to add member to cluster")
		}

		if cc.NodeID == uint64(rs.id) {
			logger.Info().Msg("I've been removed from the cluster! Shutting down.")
			return false
		}
		rs.transport.RemovePeer(etcdtypes.ID(cc.NodeID))
	}

	logger.Debug().Str("cluster", rs.cluster.toString()).Msg("after conf changed")

	rs.cluster.sendConfChangeReply(cc, member, nil)

	return true
}

// publishEntries writes committed log entries to commit channel and returns
// whether all entries could be published.
func (rs *raftServer) publishEntries(ents []raftpb.Entry) bool {
	for i := range ents {
		logger.Info().Uint64("idx", ents[i].Index).Uint64("term", ents[i].Term).Str("type", ents[i].Type.String()).Int("datalen", len(ents[i].Data)).Msg("publish entry")

		switch ents[i].Type {
		case raftpb.EntryNormal:
			var block *types.Block
			var err error
			if len(ents[i].Data) != 0 {
				if block, err = unmarshalEntryData(ents[i].Data); err != nil {
					logger.Fatal().Err(err).Uint64("idx", ents[i].Index).Uint64("term", ents[i].Term).Msg("commit entry is corrupted")
					continue
				}

			}

			if block != nil {
				logger.Info().Str("hash", block.ID()).Uint64("no", block.BlockNo()).Msg("commit normal block entry")
			}

			select {
			case rs.commitC <- block:
			case <-rs.stopc:
				return false
			}
			rs.updateBlockProgress(ents[i].Term, ents[i].Index, block)

		case raftpb.EntryConfChange:
			if !rs.applyConfChange(&ents[i]) {
				return false
			}
		}

		// after commit, update appliedIndex
		rs.setAppliedIndex(ents[i].Index)

		/* XXX no need commitC <- nil
		// special nil commit to signal replay has finished
		if ents[i].Index == rs.lastIndex {
			if !rs.startSync {
				logger.Debug().Uint64("idx", rs.lastIndex).Msg("published all entries of WAL")

				select {
				case rs.commitC <- nil:
					rs.startSync = true
				case <-rs.stopc:
					return false
				}
			}
		}*/
	}
	return true
}

func (rs *raftServer) setSnapshotIndex(idx uint64) {
	logger.Debug().Uint64("index", idx).Msg("raft server set snapshotIndex")

	rs.snapshotIndex = idx
}

func (rs *raftServer) setAppliedIndex(idx uint64) {
	logger.Debug().Uint64("index", idx).Msg("raft server set appliedIndex")

	rs.appliedIndex = idx
}

func (rs *raftServer) setConfState(state raftpb.ConfState) {
	logger.Debug().Str("state", consensus.ConfStateToString(&state)).Msg("raft server set confstate")

	rs.confState = state
}

func (rs *raftServer) Process(ctx context.Context, m raftpb.Message) error {
	return rs.node.Step(ctx, m)
}

func (rs *raftServer) IsIDRemoved(id uint64) bool {
	return rs.cluster.IsIDRemoved(id)
}

func (rs *raftServer) ReportUnreachable(id uint64) {
	logger.Debug().Str("toID", MemberIDToString(id)).Msg("report unreachable")

	rs.node.ReportUnreachable(id)
}

func (rs *raftServer) ReportSnapshot(id uint64, status raftlib.SnapshotStatus) {
	if status == raftlib.SnapshotFinish {
		logger.Debug().Str("toID", MemberIDToString(id)).Bool("isSucceed", status == raftlib.SnapshotFinish).Msg("report snapshot result")
	}

	rs.node.ReportSnapshot(id, status)
}

func (rs *raftServer) WaitStartup() {
	logger.Debug().Msg("raft start wait")
	for s := range rs.commitC {
		if s == nil {
			break
		}
	}
	logger.Debug().Msg("raft start succeed")
}

func (rs *raftServer) updateLeader(softState *raftlib.SoftState) {
	if softState.Lead != rs.GetLeader() {
		atomic.StoreUint64(&rs.leaderStatus.leader, softState.Lead)

		rs.leaderStatus.leaderChanged++

		logger.Info().Str("ID", MemberIDToString(rs.id)).Str("leader", MemberIDToString(softState.Lead)).Msg("leader changed")
	}
}

func (rs *raftServer) GetLeader() uint64 {
	return atomic.LoadUint64(&rs.leaderStatus.leader)
}

func (rs *raftServer) IsLeader() bool {
	return rs.id != consensus.InvalidMemberID && rs.id == rs.GetLeader()
}

func (rs *raftServer) Status() raftlib.Status {
	node := rs.getNodeSync()
	if node == nil {
		return raftlib.Status{}
	}

	return node.Status()
}

// GetExistingCluster returns information of existing cluster.
// It request member info to all of peers.
func (rs *raftServer) GetExistingCluster() (*Cluster, error) {
	var (
		cl  *Cluster
		err error
	)
	for i := 1; i <= MaxTryGetCluster; i++ {
		cl, err = GetClusterInfo(rs.ComponentHub)
		if err != nil {
			if err != ErrGetClusterTimeout && i != MaxTryGetCluster {
				logger.Debug().Err(err).Int("try", i).Msg("failed try to get cluster. and sleep")
				time.Sleep(time.Second * 10)
			} else {
				logger.Warn().Err(err).Int("try", i).Msg("failed try to get cluster")
			}
			continue
		}

		return cl, nil
	}

	return nil, ErrGetClusterFail
}

func marshalEntryData(block *types.Block) ([]byte, error) {
	var data []byte
	var err error
	if data, err = proto.Marshal(block); err != nil {
		logger.Fatal().Err(err).Msg("poposed data is invalid")
	}

	return data, nil
}

var (
	ErrUnmarshal = errors.New("failed to unmarshalEntryData log entry")
)

func unmarshalEntryData(data []byte) (*types.Block, error) {
	block := &types.Block{}
	if err := proto.Unmarshal(data, block); err != nil {
		return block, ErrUnmarshal
	}

	return block, nil
}
