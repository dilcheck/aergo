/**
 *  @file
 *  @copyright defined in aergo/LICENSE.txt
 */
package system

import (
	"encoding/json"
	"math/big"

	"github.com/aergoio/aergo/state"
	"github.com/aergoio/aergo/types"
)

type SystemContext struct {
	BlockNo  uint64
	Call     *types.CallInfo
	Args     []string
	Staked   *types.Staking
	Vote     *types.Vote
	Sender   *state.V
	Receiver *state.V
}

func ExecuteSystemTx(scs *state.ContractState, txBody *types.TxBody,
	sender, receiver *state.V, blockNo types.BlockNo) ([]*types.Event, error) {

	context, err := ValidateSystemTx(sender.ID(), txBody, sender, scs, blockNo)
	if err != nil {
		return nil, err
	}
	context.Receiver = receiver

	var event *types.Event
	switch context.Call.Name {
	case types.Stake:
		event, err = staking(txBody, sender, receiver, scs, blockNo, context)
	case types.VoteBP:
		event, err = voting(txBody, sender, receiver, scs, blockNo, context)
	case types.Unstake:
		event, err = unstaking(txBody, sender, receiver, scs, blockNo, context)
	default:
		err = types.ErrTxInvalidPayload
	}
	if err != nil {
		return nil, err
	}
	var events []*types.Event
	events = append(events, event)
	return events, nil
}

func GetNamePrice(scs *state.ContractState) *big.Int {
	votelist, err := getVoteResult(scs, []byte(types.VoteNamePrice[2:]), 1)
	if err != nil {
		panic("could not get vote result for min staking")
	}
	if len(votelist.Votes) == 0 {
		return types.NamePrice
	}
	return new(big.Int).SetBytes(votelist.Votes[0].GetCandidate())
}

func GetMinimumStaking(scs *state.ContractState) *big.Int {
	votelist, err := getVoteResult(scs, []byte(types.VoteMinStaking[2:]), 1)
	if err != nil {
		panic("could not get vote result for min staking")
	}
	if len(votelist.Votes) == 0 {
		return types.StakingMinimum
	}
	minimumStaking, ok := new(big.Int).SetString(string(votelist.Votes[0].GetCandidate()), 10)
	if !ok {
		panic("could not get vote result for min staking")
	}
	return minimumStaking
}

func ValidateSystemTx(account []byte, txBody *types.TxBody, sender *state.V,
	scs *state.ContractState, blockNo uint64) (*SystemContext, error) {
	var ci types.CallInfo
	context := &SystemContext{Call: &ci, Sender: sender, BlockNo: blockNo}

	if err := json.Unmarshal(txBody.Payload, &ci); err != nil {
		return nil, types.ErrTxInvalidPayload
	}
	switch ci.Name {
	case types.Stake:
		if sender != nil && sender.Balance().Cmp(txBody.GetAmountBigInt()) < 0 {
			return nil, types.ErrInsufficientBalance
		}
		staked, err := validateForStaking(account, txBody, scs, blockNo)
		if err != nil {
			return nil, err
		}
		context.Staked = staked
	case types.VoteBP:
		staked, err := getStaking(scs, account)
		if err != nil {
			return nil, err
		}
		if staked.GetAmountBigInt().Cmp(new(big.Int).SetUint64(0)) == 0 {
			return nil, types.ErrMustStakeBeforeVote
		}
		oldvote, err := GetVote(scs, account, []byte(ci.Name[2:]))
		if err != nil {
			return nil, err
		}
		if oldvote.Amount != nil && staked.GetWhen()+VotingDelay > blockNo {
			return nil, types.ErrLessTimeHasPassed
		}
		context.Staked = staked
		context.Vote = oldvote
	case types.Unstake:
		staked, err := validateForUnstaking(account, txBody, scs, blockNo)
		if err != nil {
			return nil, err
		}
		context.Staked = staked
	default:
		return nil, types.ErrTxInvalidPayload
	}
	return context, nil
}

func validateForStaking(account []byte, txBody *types.TxBody, scs *state.ContractState, blockNo uint64) (*types.Staking, error) {
	staked, err := getStaking(scs, account)
	if err != nil {
		return nil, err
	}
	if staked.GetAmount() != nil && staked.GetWhen()+StakingDelay > blockNo {
		return nil, types.ErrLessTimeHasPassed
	}
	toBe := new(big.Int).Add(staked.GetAmountBigInt(), txBody.GetAmountBigInt())
	if GetMinimumStaking(scs).Cmp(toBe) > 0 {
		return nil, types.ErrTooSmallAmount
	}
	return staked, nil
}

func validateForUnstaking(account []byte, txBody *types.TxBody, scs *state.ContractState, blockNo uint64) (*types.Staking, error) {
	staked, err := getStaking(scs, account)
	if err != nil {
		return nil, err
	}
	if staked.GetAmountBigInt().Cmp(big.NewInt(0)) == 0 {
		return nil, types.ErrMustStakeBeforeUnstake
	}
	if staked.GetAmountBigInt().Cmp(txBody.GetAmountBigInt()) < 0 {
		return nil, types.ErrExceedAmount
	}
	if staked.GetWhen()+StakingDelay > blockNo {
		return nil, types.ErrLessTimeHasPassed
	}
	toBe := new(big.Int).Sub(staked.GetAmountBigInt(), txBody.GetAmountBigInt())
	if toBe.Cmp(big.NewInt(0)) != 0 && GetMinimumStaking(scs).Cmp(toBe) > 0 {
		return nil, types.ErrTooSmallAmount
	}
	return staked, nil
}
