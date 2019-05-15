package chain

import (
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"
)

type ErrDebug struct {
	cond  StopCond
	value int
}

type StopCond int

// stop before swap chain
const (
	DEBUG_CHAIN_STOP StopCond = 0 + iota
	DEBUG_CHAIN_RANDOM_STOP
	DEBUG_CHAIN_SLEEP
	DEBUG_SYNCER_CRASH
)

const (
	DEBUG_CHAIN_STOP_INF = DEBUG_SYNCER_CRASH
)

var (
	EnvNameStaticCrash     = "DEBUG_CHAIN_CRASH"       // 1 ~ 4
	EnvNameRandomCrashTime = "DEBUG_RANDOM_CRASH_TIME" // 1 ~ 600000(=10min) ms
	EnvNameChainSleep      = "DEBUG_CHAIN_SLEEP"       // sleep before connecting block for each block (ms). used
	EnvNameSyncCrash       = "DEBUG_SYNCER_CRASH"      // case 1
)

var stopConds = [...]string{
	EnvNameStaticCrash,
	EnvNameRandomCrashTime,
	EnvNameChainSleep,
	EnvNameSyncCrash,
}

func (c StopCond) String() string { return stopConds[c] }

func (ec *ErrDebug) Error() string {
	return fmt.Sprintf("stopped by debugger cond[%s]=%d", ec.cond.String(), ec.value)
}

type Debugger struct {
	sync.RWMutex
	condMap map[StopCond]int
	isEnv   map[StopCond]bool
}

func newDebugger() *Debugger {
	dbg := &Debugger{condMap: make(map[StopCond]int), isEnv: make(map[StopCond]bool)}

	checkEnv := func(condName StopCond) {
		envName := stopConds[condName]

		envStr := os.Getenv(envName)
		if len(envStr) > 0 {
			val, err := strconv.Atoi(envStr)
			if err != nil {
				logger.Error().Err(err).Msgf("%s environment varialble must be integer", envName)
				return
			}
			logger.Debug().Int("value", val).Msgf("env variable[%s] is set", envName)

			dbg.set(condName, val, true)
		}
	}

	checkEnv(DEBUG_CHAIN_STOP)
	checkEnv(DEBUG_CHAIN_RANDOM_STOP)
	checkEnv(DEBUG_CHAIN_SLEEP)
	checkEnv(DEBUG_SYNCER_CRASH)

	return dbg
}

func (debug *Debugger) set(cond StopCond, value int, env bool) {
	if debug == nil {
		return
	}

	debug.Lock()
	defer debug.Unlock()

	logger.Debug().Int("cond", int(cond)).Str("name", stopConds[cond]).Int("val", value).Msg("set debug condition")

	debug.condMap[cond] = value
	debug.isEnv[cond] = env
}

func (debug *Debugger) unset(cond StopCond) {
	if debug == nil {
		return
	}

	debug.Lock()
	defer debug.Unlock()

	delete(debug.condMap, cond)
}

func (debug *Debugger) clear() {
	if debug == nil {
		return
	}

	debug.Lock()
	defer debug.Unlock()

	debug.condMap = make(map[StopCond]int)
	debug.isEnv = make(map[StopCond]bool)
}

func (debug *Debugger) Check(cond StopCond, value int) error {
	if debug == nil {
		return nil
	}

	debug.Lock()
	defer debug.Unlock()

	if setVal, ok := debug.condMap[cond]; ok {
		switch cond {
		case DEBUG_CHAIN_STOP:
			if setVal == value {
				if debug.isEnv[cond] {
					logger.Fatal().Str("cond", stopConds[cond]).Msg("shutdown by DEBUG_CHAIN_CRASH")
				} else {
					return &ErrDebug{cond: cond, value: value}
				}
			}

		case DEBUG_CHAIN_RANDOM_STOP:
			go crashRandom(setVal)
			handleCrashRandom(setVal)

		case DEBUG_CHAIN_SLEEP:
			handleChainSleep(setVal)

		case DEBUG_SYNCER_CRASH:
			handleSyncerCrash(setVal)
		}
	}

	return nil
}

func handleChainSleep(sleepMils int) {
	logger.Debug().Int("sleep(ms)", sleepMils).Msg("before chain sleep")

	time.Sleep(time.Millisecond * time.Duration(sleepMils))

	logger.Debug().Msg("after chain sleep")
}

func handleCrashRandom(waitMils int) {
	logger.Debug().Int("after(ms)", waitMils).Msg("before random crash")

	go crashRandom(waitMils)
}

func handleSyncerCrash(val int) {
	logger.Fatal().Int("val", val).Msg("sync crash by DEBUG_SYNC_CRASH")
}

func crashRandom(waitMils int) {
	if waitMils <= 0 {
		return
	}

	time.Sleep(time.Millisecond * time.Duration(waitMils))

	logger.Debug().Msg("shutdown by DEBUG_RANDOM_CRASH_TIME")

	os.Exit(100)
}