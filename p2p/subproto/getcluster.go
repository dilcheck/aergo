/*
 * @file
 * @copyright defined in aergo/LICENSE.txt
 */

package subproto

import (
	"errors"
	"github.com/aergoio/aergo-lib/log"
	"github.com/aergoio/aergo/consensus"
	"github.com/aergoio/aergo/p2p/p2pcommon"
	"github.com/aergoio/aergo/p2p/p2putil"
	"github.com/aergoio/aergo/types"
)

var (
	ErrConsensusAccessorNotReady = errors.New("consensus acessor is not ready")
)

type getClusterRequestHandler struct {
	BaseMsgHandler

	consAcc consensus.ConsensusAccessor
}

var _ p2pcommon.MessageHandler = (*getClusterRequestHandler)(nil)

type getClusterResponseHandler struct {
	BaseMsgHandler
}

var _ p2pcommon.MessageHandler = (*getClusterResponseHandler)(nil)

// NewGetClusterReqHandler creates handler for PingRequest
func NewGetClusterReqHandler(pm p2pcommon.PeerManager, peer p2pcommon.RemotePeer, logger *log.Logger, actor p2pcommon.ActorService, consAcc consensus.ConsensusAccessor) *getClusterRequestHandler {
	ph := &getClusterRequestHandler{
		BaseMsgHandler: BaseMsgHandler{protocol: GetClusterRequest, pm: pm, peer: peer, actor: actor, logger: logger},
		consAcc:        consAcc,
	}
	return ph
}

func (ph *getClusterRequestHandler) ParsePayload(rawbytes []byte) (p2pcommon.MessageBody, error) {
	return p2putil.UnmarshalAndReturn(rawbytes, &types.GetClusterInfoRequest{})
}

func (ph *getClusterRequestHandler) Handle(msg p2pcommon.Message, msgBody p2pcommon.MessageBody) {
	//peerID := ph.peer.ID()
	remotePeer := ph.peer
	data := msgBody.(*types.GetClusterInfoRequest)
	p2putil.DebugLogReceiveMsg(ph.logger, ph.protocol, msg.ID().String(), remotePeer, data.String())

	resp := &types.GetClusterInfoResponse{}

	// GetClusterInfo from consensus
	if ph.consAcc == nil {
		resp.Error = ErrConsensusAccessorNotReady.Error()
	} else {
		mbrs, chainID, err := ph.consAcc.ClusterInfo()
		if err != nil {
			resp.Error = err.Error()
		} else {
			resp.MbrAttrs = mbrs
			resp.ChainID = chainID
		}
	}

	remotePeer.SendMessage(remotePeer.MF().NewMsgResponseOrder(msg.ID(), GetClusterResponse, resp))
}

// NewGetClusterRespHandler creates handler for PingRequest
func NewGetClusterRespHandler(pm p2pcommon.PeerManager, peer p2pcommon.RemotePeer, logger *log.Logger, actor p2pcommon.ActorService) *getClusterResponseHandler {
	ph := &getClusterResponseHandler{BaseMsgHandler{protocol: GetClusterResponse, pm: pm, peer: peer, actor: actor, logger: logger}}
	return ph
}

func (ph *getClusterResponseHandler) ParsePayload(rawbytes []byte) (p2pcommon.MessageBody, error) {
	return p2putil.UnmarshalAndReturn(rawbytes, &types.GetClusterInfoResponse{})
}

func (ph *getClusterResponseHandler) Handle(msg p2pcommon.Message, msgBody p2pcommon.MessageBody) {
	remotePeer := ph.peer
	data := msgBody.(*types.GetClusterInfoResponse)
	p2putil.DebugLogReceiveResponseMsg(ph.logger, ph.protocol, msg.ID().String(), msg.OriginalID().String(), remotePeer, data.String())

	if !remotePeer.GetReceiver(msg.OriginalID())(msg, data) {
		// ignore dangling response
		// TODO add penalty if needed
		remotePeer.ConsumeRequest(msg.OriginalID())
	}

}
