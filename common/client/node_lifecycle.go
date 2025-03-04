package client

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/big"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	bigmath "github.com/smartcontractkit/chainlink-common/pkg/utils/big_math"

	"github.com/smartcontractkit/chainlink/v2/core/utils"
)

var (
	promPoolRPCNodeHighestSeenBlock = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "pool_rpc_node_highest_seen_block",
		Help: "The highest seen block for the given RPC node",
	}, []string{"chainID", "nodeName"})
	promPoolRPCNodeNumSeenBlocks = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "pool_rpc_node_num_seen_blocks",
		Help: "The total number of new blocks seen by the given RPC node",
	}, []string{"chainID", "nodeName"})
	promPoolRPCNodePolls = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "pool_rpc_node_polls_total",
		Help: "The total number of poll checks for the given RPC node",
	}, []string{"chainID", "nodeName"})
	promPoolRPCNodePollsFailed = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "pool_rpc_node_polls_failed",
		Help: "The total number of failed poll checks for the given RPC node",
	}, []string{"chainID", "nodeName"})
	promPoolRPCNodePollsSuccess = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "pool_rpc_node_polls_success",
		Help: "The total number of successful poll checks for the given RPC node",
	}, []string{"chainID", "nodeName"})
)

// zombieNodeCheckInterval controls how often to re-check to see if we need to
// state change in case we have to force a state transition due to no available
// nodes.
// NOTE: This only applies to out-of-sync nodes if they are the last available node
func zombieNodeCheckInterval(noNewHeadsThreshold time.Duration) time.Duration {
	interval := noNewHeadsThreshold
	if interval <= 0 || interval > QueryTimeout {
		interval = QueryTimeout
	}
	return utils.WithJitter(interval)
}

func (n *node[CHAIN_ID, HEAD, RPC]) setLatestReceived(blockNumber int64, totalDifficulty *big.Int) {
	n.stateMu.Lock()
	defer n.stateMu.Unlock()
	n.stateLatestBlockNumber = blockNumber
	n.stateLatestTotalDifficulty = totalDifficulty
}

const (
	msgCannotDisable = "but cannot disable this connection because there are no other RPC endpoints, or all other RPC endpoints are dead."
	msgDegradedState = "Chainlink is now operating in a degraded state and urgent action is required to resolve the issue"
)

const rpcSubscriptionMethodNewHeads = "newHeads"

// Node is a FSM
// Each state has a loop that goes with it, which monitors the node and moves it into another state as necessary.
// Only one loop must run at a time.
// Each loop passes control onto the next loop as it exits, except when the node is Closed which terminates the loop permanently.

// This handles node lifecycle for the ALIVE state
// Should only be run ONCE per node, after a successful Dial
func (n *node[CHAIN_ID, HEAD, RPC]) aliveLoop() {
	defer n.wg.Done()

	{
		// sanity check
		state := n.State()
		switch state {
		case nodeStateAlive:
		case nodeStateClosed:
			return
		default:
			panic(fmt.Sprintf("aliveLoop can only run for node in Alive state, got: %s", state))
		}
	}

	noNewHeadsTimeoutThreshold := n.noNewHeadsThreshold
	pollFailureThreshold := n.nodePoolCfg.PollFailureThreshold()
	pollInterval := n.nodePoolCfg.PollInterval()

	lggr := logger.Named(n.lfcLog, "Alive")
	lggr = logger.With(lggr, "noNewHeadsTimeoutThreshold", noNewHeadsTimeoutThreshold, "pollInterval", pollInterval, "pollFailureThreshold", pollFailureThreshold)
	logger.Tracew(lggr, "Alive loop starting", "nodeState", n.State())

	headsC := make(chan HEAD)
	sub, err := n.rpc.Subscribe(n.nodeCtx, headsC, rpcSubscriptionMethodNewHeads)
	if err != nil {
		lggr.Errorw("Initial subscribe for heads failed", "nodeState", n.State())
		n.declareUnreachable()
		return
	}
	// TODO: nit fix. If multinode switches primary node before we set sub as AliveSub, sub will be closed and we'll
	// falsely transition this node to unreachable state
	n.rpc.SetAliveLoopSub(sub)
	defer sub.Unsubscribe()

	var outOfSyncT *time.Ticker
	var outOfSyncTC <-chan time.Time
	if noNewHeadsTimeoutThreshold > 0 {
		lggr.Debugw("Head liveness checking enabled", "nodeState", n.State())
		outOfSyncT = time.NewTicker(noNewHeadsTimeoutThreshold)
		defer outOfSyncT.Stop()
		outOfSyncTC = outOfSyncT.C
	} else {
		lggr.Debug("Head liveness checking disabled")
	}

	var pollCh <-chan time.Time
	if pollInterval > 0 {
		lggr.Debug("Polling enabled")
		pollT := time.NewTicker(pollInterval)
		defer pollT.Stop()
		pollCh = pollT.C
		if pollFailureThreshold > 0 {
			// polling can be enabled with no threshold to enable polling but
			// the node will not be marked offline regardless of the number of
			// poll failures
			lggr.Debug("Polling liveness checking enabled")
		}
	} else {
		lggr.Debug("Polling disabled")
	}

	_, highestReceivedBlockNumber, _ := n.StateAndLatest()
	var pollFailures uint32

	for {
		select {
		case <-n.nodeCtx.Done():
			return
		case <-pollCh:
			var version string
			promPoolRPCNodePolls.WithLabelValues(n.chainID.String(), n.name).Inc()
			logger.Tracew(lggr, "Polling for version", "nodeState", n.State(), "pollFailures", pollFailures)
			ctx, cancel := context.WithTimeout(n.nodeCtx, pollInterval)
			version, err := n.RPC().ClientVersion(ctx)
			cancel()
			if err != nil {
				// prevent overflow
				if pollFailures < math.MaxUint32 {
					promPoolRPCNodePollsFailed.WithLabelValues(n.chainID.String(), n.name).Inc()
					pollFailures++
				}
				lggr.Warnw(fmt.Sprintf("Poll failure, RPC endpoint %s failed to respond properly", n.String()), "err", err, "pollFailures", pollFailures, "nodeState", n.State())
			} else {
				lggr.Debugw("Version poll successful", "nodeState", n.State(), "clientVersion", version)
				promPoolRPCNodePollsSuccess.WithLabelValues(n.chainID.String(), n.name).Inc()
				pollFailures = 0
			}
			if pollFailureThreshold > 0 && pollFailures >= pollFailureThreshold {
				lggr.Errorw(fmt.Sprintf("RPC endpoint failed to respond to %d consecutive polls", pollFailures), "pollFailures", pollFailures, "nodeState", n.State())
				if n.nLiveNodes != nil {
					if l, _, _ := n.nLiveNodes(); l < 2 {
						logger.Criticalf(lggr, "RPC endpoint failed to respond to polls; %s %s", msgCannotDisable, msgDegradedState)
						continue
					}
				}
				n.declareUnreachable()
				return
			}
			_, num, td := n.StateAndLatest()
			if outOfSync, liveNodes := n.syncStatus(num, td); outOfSync {
				// note: there must be another live node for us to be out of sync
				lggr.Errorw("RPC endpoint has fallen behind", "blockNumber", num, "totalDifficulty", td, "nodeState", n.State())
				if liveNodes < 2 {
					logger.Criticalf(lggr, "RPC endpoint has fallen behind; %s %s", msgCannotDisable, msgDegradedState)
					continue
				}
				n.declareOutOfSync(n.isOutOfSync)
				return
			}
		case bh, open := <-headsC:
			if !open {
				lggr.Errorw("Subscription channel unexpectedly closed", "nodeState", n.State())
				n.declareUnreachable()
				return
			}
			promPoolRPCNodeNumSeenBlocks.WithLabelValues(n.chainID.String(), n.name).Inc()
			logger.Tracew(lggr, "Got head", "head", bh)
			if bh.BlockNumber() > highestReceivedBlockNumber {
				promPoolRPCNodeHighestSeenBlock.WithLabelValues(n.chainID.String(), n.name).Set(float64(bh.BlockNumber()))
				logger.Tracew(lggr, "Got higher block number, resetting timer", "latestReceivedBlockNumber", highestReceivedBlockNumber, "blockNumber", bh.BlockNumber(), "nodeState", n.State())
				highestReceivedBlockNumber = bh.BlockNumber()
			} else {
				logger.Tracew(lggr, "Ignoring previously seen block number", "latestReceivedBlockNumber", highestReceivedBlockNumber, "blockNumber", bh.BlockNumber(), "nodeState", n.State())
			}
			if outOfSyncT != nil {
				outOfSyncT.Reset(noNewHeadsTimeoutThreshold)
			}
			n.setLatestReceived(bh.BlockNumber(), bh.BlockDifficulty())
		case err := <-sub.Err():
			lggr.Errorw("Subscription was terminated", "err", err, "nodeState", n.State())
			n.declareUnreachable()
			return
		case <-outOfSyncTC:
			// We haven't received a head on the channel for at least the
			// threshold amount of time, mark it broken
			lggr.Errorw(fmt.Sprintf("RPC endpoint detected out of sync; no new heads received for %s (last head received was %v)", noNewHeadsTimeoutThreshold, highestReceivedBlockNumber), "nodeState", n.State(), "latestReceivedBlockNumber", highestReceivedBlockNumber, "noNewHeadsTimeoutThreshold", noNewHeadsTimeoutThreshold)
			if n.nLiveNodes != nil {
				if l, _, _ := n.nLiveNodes(); l < 2 {
					logger.Criticalf(lggr, "RPC endpoint detected out of sync; %s %s", msgCannotDisable, msgDegradedState)
					// We don't necessarily want to wait the full timeout to check again, we should
					// check regularly and log noisily in this state
					outOfSyncT.Reset(zombieNodeCheckInterval(n.noNewHeadsThreshold))
					continue
				}
			}
			n.declareOutOfSync(func(num int64, td *big.Int) bool { return num < highestReceivedBlockNumber })
			return
		}
	}
}

func (n *node[CHAIN_ID, HEAD, RPC]) isOutOfSync(num int64, td *big.Int) (outOfSync bool) {
	outOfSync, _ = n.syncStatus(num, td)
	return
}

// syncStatus returns outOfSync true if num or td is more than SyncThresold behind the best node.
// Always returns outOfSync false for SyncThreshold 0.
// liveNodes is only included when outOfSync is true.
func (n *node[CHAIN_ID, HEAD, RPC]) syncStatus(num int64, td *big.Int) (outOfSync bool, liveNodes int) {
	if n.nLiveNodes == nil {
		return // skip for tests
	}
	threshold := n.nodePoolCfg.SyncThreshold()
	if threshold == 0 {
		return // disabled
	}
	// Check against best node
	ln, highest, greatest := n.nLiveNodes()
	mode := n.nodePoolCfg.SelectionMode()
	switch mode {
	case NodeSelectionModeHighestHead, NodeSelectionModeRoundRobin, NodeSelectionModePriorityLevel:
		return num < highest-int64(threshold), ln
	case NodeSelectionModeTotalDifficulty:
		bigThreshold := big.NewInt(int64(threshold))
		return td.Cmp(bigmath.Sub(greatest, bigThreshold)) < 0, ln
	default:
		panic("unrecognized NodeSelectionMode: " + mode)
	}
}

const (
	msgReceivedBlock = "Received block for RPC node, waiting until back in-sync to mark as live again"
	msgInSync        = "RPC node back in sync"
)

// outOfSyncLoop takes an OutOfSync node and waits until isOutOfSync returns false to go back to live status
func (n *node[CHAIN_ID, HEAD, RPC]) outOfSyncLoop(isOutOfSync func(num int64, td *big.Int) bool) {
	defer n.wg.Done()

	{
		// sanity check
		state := n.State()
		switch state {
		case nodeStateOutOfSync:
		case nodeStateClosed:
			return
		default:
			panic(fmt.Sprintf("outOfSyncLoop can only run for node in OutOfSync state, got: %s", state))
		}
	}

	outOfSyncAt := time.Now()

	lggr := logger.Named(n.lfcLog, "OutOfSync")
	lggr.Debugw("Trying to revive out-of-sync RPC node", "nodeState", n.State())

	// Need to redial since out-of-sync nodes are automatically disconnected
	if err := n.rpc.Dial(n.nodeCtx); err != nil {
		lggr.Errorw("Failed to dial out-of-sync RPC node", "nodeState", n.State())
		n.declareUnreachable()
		return
	}

	// Manually re-verify since out-of-sync nodes are automatically disconnected
	if err := n.verify(n.nodeCtx); err != nil {
		lggr.Errorw(fmt.Sprintf("Failed to verify out-of-sync RPC node: %v", err), "err", err)
		n.declareInvalidChainID()
		return
	}

	logger.Tracew(lggr, "Successfully subscribed to heads feed on out-of-sync RPC node", "nodeState", n.State())

	ch := make(chan HEAD)
	sub, err := n.rpc.Subscribe(n.nodeCtx, ch, rpcSubscriptionMethodNewHeads)
	if err != nil {
		lggr.Errorw("Failed to subscribe heads on out-of-sync RPC node", "nodeState", n.State(), "err", err)
		n.declareUnreachable()
		return
	}
	defer sub.Unsubscribe()

	for {
		select {
		case <-n.nodeCtx.Done():
			return
		case head, open := <-ch:
			if !open {
				lggr.Error("Subscription channel unexpectedly closed", "nodeState", n.State())
				n.declareUnreachable()
				return
			}
			n.setLatestReceived(head.BlockNumber(), head.BlockDifficulty())
			if !isOutOfSync(head.BlockNumber(), head.BlockDifficulty()) {
				// back in-sync! flip back into alive loop
				lggr.Infow(fmt.Sprintf("%s: %s. Node was out-of-sync for %s", msgInSync, n.String(), time.Since(outOfSyncAt)), "blockNumber", head.BlockNumber(), "blockDifficulty", head.BlockDifficulty(), "nodeState", n.State())
				n.declareInSync()
				return
			}
			lggr.Debugw(msgReceivedBlock, "blockNumber", head.BlockNumber(), "blockDifficulty", head.BlockDifficulty(), "nodeState", n.State())
		case <-time.After(zombieNodeCheckInterval(n.noNewHeadsThreshold)):
			if n.nLiveNodes != nil {
				if l, _, _ := n.nLiveNodes(); l < 1 {
					logger.Critical(lggr, "RPC endpoint is still out of sync, but there are no other available nodes. This RPC node will be forcibly moved back into the live pool in a degraded state")
					n.declareInSync()
					return
				}
			}
		case err := <-sub.Err():
			lggr.Errorw("Subscription was terminated", "nodeState", n.State(), "err", err)
			n.declareUnreachable()
			return
		}
	}
}

func (n *node[CHAIN_ID, HEAD, RPC]) unreachableLoop() {
	defer n.wg.Done()

	{
		// sanity check
		state := n.State()
		switch state {
		case nodeStateUnreachable:
		case nodeStateClosed:
			return
		default:
			panic(fmt.Sprintf("unreachableLoop can only run for node in Unreachable state, got: %s", state))
		}
	}

	unreachableAt := time.Now()

	lggr := logger.Named(n.lfcLog, "Unreachable")
	lggr.Debugw("Trying to revive unreachable RPC node", "nodeState", n.State())

	dialRetryBackoff := utils.NewRedialBackoff()

	for {
		select {
		case <-n.nodeCtx.Done():
			return
		case <-time.After(dialRetryBackoff.Duration()):
			logger.Tracew(lggr, "Trying to re-dial RPC node", "nodeState", n.State())

			err := n.rpc.Dial(n.nodeCtx)
			if err != nil {
				lggr.Errorw(fmt.Sprintf("Failed to redial RPC node; still unreachable: %v", err), "err", err, "nodeState", n.State())
				continue
			}

			n.setState(nodeStateDialed)

			err = n.verify(n.nodeCtx)

			if errors.Is(err, errInvalidChainID) {
				lggr.Errorw("Failed to redial RPC node; remote endpoint returned the wrong chain ID", "err", err)
				n.declareInvalidChainID()
				return
			} else if err != nil {
				lggr.Errorw(fmt.Sprintf("Failed to redial RPC node; verify failed: %v", err), "err", err)
				n.declareUnreachable()
				return
			}

			lggr.Infow(fmt.Sprintf("Successfully redialled and verified RPC node %s. Node was offline for %s", n.String(), time.Since(unreachableAt)), "nodeState", n.State())
			n.declareAlive()
			return
		}
	}
}

func (n *node[CHAIN_ID, HEAD, RPC]) invalidChainIDLoop() {
	defer n.wg.Done()

	{
		// sanity check
		state := n.State()
		switch state {
		case nodeStateInvalidChainID:
		case nodeStateClosed:
			return
		default:
			panic(fmt.Sprintf("invalidChainIDLoop can only run for node in InvalidChainID state, got: %s", state))
		}
	}

	invalidAt := time.Now()

	lggr := logger.Named(n.lfcLog, "InvalidChainID")
	lggr.Debugw(fmt.Sprintf("Periodically re-checking RPC node %s with invalid chain ID", n.String()), "nodeState", n.State())

	chainIDRecheckBackoff := utils.NewRedialBackoff()

	for {
		select {
		case <-n.nodeCtx.Done():
			return
		case <-time.After(chainIDRecheckBackoff.Duration()):
			err := n.verify(n.nodeCtx)
			if errors.Is(err, errInvalidChainID) {
				lggr.Errorw("Failed to verify RPC node; remote endpoint returned the wrong chain ID", "err", err)
				continue
			} else if err != nil {
				lggr.Errorw(fmt.Sprintf("Unexpected error while verifying RPC node chain ID; %v", err), "err", err)
				n.declareUnreachable()
				return
			}
			lggr.Infow(fmt.Sprintf("Successfully verified RPC node. Node was offline for %s", time.Since(invalidAt)), "nodeState", n.State())
			n.declareAlive()
			return
		}
	}
}
