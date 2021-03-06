/**
 *  @file
 *  @copyright defined in aergo/LICENSE.txt
 */
package system

import (
	"math/big"
	"testing"

	"github.com/aergoio/aergo/types"
	"github.com/mr-tron/base58/base58"
	"github.com/stretchr/testify/assert"
)

func TestBasicExecute(t *testing.T) {
	scs, sender, receiver := initTest(t)
	defer deinitTest()

	tx := &types.Tx{
		Body: &types.TxBody{
			Account:   sender.ID(),
			Recipient: []byte(types.AergoSystem),
			Amount:    types.StakingMinimum.Bytes(),
			Payload:   []byte(`{"Name":"v1stake"}`),
		},
	}
	sender.AddBalance(types.StakingMinimum)

	emptytx := &types.TxBody{}
	_, err := ExecuteSystemTx(scs, emptytx, sender, receiver, 0)
	assert.EqualError(t, types.ErrTxInvalidPayload, err.Error(), "Execute system tx failed")

	events, err := ExecuteSystemTx(scs, tx.GetBody(), sender, receiver, 0)
	assert.NoError(t, err, "Execute system tx failed in staking")
	assert.Equal(t, sender.Balance().Uint64(), uint64(0), "sender.Balance() should be 0 after staking")
	assert.Equal(t, events[0].ContractAddress, types.AddressPadding([]byte(types.AergoSystem)), "check event")
	assert.Equal(t, events[0].EventName, types.Stake[2:], "check event")
	staking, err := getStaking(scs, tx.GetBody().GetAccount())
	assert.Equal(t, types.StakingMinimum, new(big.Int).SetBytes(staking.Amount), "check amount of staking")

	tx.Body.Payload = []byte(`{"Name":"v1voteBP","Args":["16Uiu2HAmBDcLEjBYeEnGU2qDD1KdpEdwDBtN7gqXzNZbHXo8Q841"]}`)
	tx.Body.Amount = big.NewInt(0).Bytes()
	events, err = ExecuteSystemTx(scs, tx.GetBody(), sender, receiver, VotingDelay)
	assert.NoError(t, err, "Execute system tx failed in voting")
	assert.Equal(t, events[0].ContractAddress, types.AddressPadding([]byte(types.AergoSystem)), "check event")
	assert.Equal(t, events[0].EventName, types.VoteBP[2:], "check event")
	tx.Body.Payload = []byte(`{"Name":"v1unstake"}`)
	tx.Body.Amount = types.StakingMinimum.Bytes()
	_, err = ExecuteSystemTx(scs, tx.GetBody(), sender, receiver, VotingDelay+StakingDelay)
	assert.NoError(t, err, "Execute system tx failed in unstaking")
	assert.Equal(t, types.StakingMinimum.Bytes(), sender.Balance().Bytes(),
		"sender.Balance() should be turn back")
	staking, err = getStaking(scs, tx.GetBody().GetAccount())
	assert.Equal(t, big.NewInt(0), new(big.Int).SetBytes(staking.Amount), "check amount of staking")
}

func TestBalanceExecute(t *testing.T) {
	scs, sender, receiver := initTest(t)
	defer deinitTest()

	tx := &types.Tx{
		Body: &types.TxBody{
			Account:   sender.ID(),
			Recipient: []byte(types.AergoSystem),
			Amount:    types.StakingMinimum.Bytes(),
			Payload:   []byte(`{"Name":"v1stake"}`),
		},
	}
	balance3 := new(big.Int).Mul(types.StakingMinimum, big.NewInt(3))
	balance2 := new(big.Int).Mul(types.StakingMinimum, big.NewInt(2))
	sender.AddBalance(balance3)

	blockNo := uint64(0)
	//staking 1
	//balance 3-1=2
	events, err := ExecuteSystemTx(scs, tx.GetBody(), sender, receiver, blockNo)
	assert.NoError(t, err, "Execute system tx failed in staking")
	assert.Equal(t, balance2, sender.Balance(), "sender.Balance() should be 0 after staking")
	assert.Equal(t, events[0].ContractAddress, types.AddressPadding([]byte(types.AergoSystem)), "check event")
	assert.Equal(t, events[0].EventName, types.Stake[2:], "check event")
	staking, err := getStaking(scs, tx.GetBody().GetAccount())
	assert.Equal(t, types.StakingMinimum, new(big.Int).SetBytes(staking.Amount), "check amount of staking")
	assert.Equal(t, types.StakingMinimum, receiver.Balance(), "check amount of staking")

	tx.Body.Payload = []byte(`{"Name":"v1voteBP","Args":["16Uiu2HAmBDcLEjBYeEnGU2qDD1KdpEdwDBtN7gqXzNZbHXo8Q841"]}`)
	tx.Body.Amount = big.NewInt(0).Bytes()

	blockNo += VotingDelay
	//voting when 1
	events, err = ExecuteSystemTx(scs, tx.GetBody(), sender, receiver, blockNo)
	assert.NoError(t, err, "Execute system tx failed in voting")
	assert.Equal(t, events[0].ContractAddress, types.AddressPadding([]byte(types.AergoSystem)), "check event")
	assert.Equal(t, events[0].EventName, types.VoteBP[2:], "check event")

	voteResult, err := getVoteResult(scs, defaultVoteKey, 1)
	assert.NoError(t, err, "get vote result")
	assert.Equal(t, types.StakingMinimum, new(big.Int).SetBytes(voteResult.Votes[0].Amount), "")

	tx.Body.Payload = []byte(`{"Name":"v1stake"}`)
	tx.Body.Amount = balance2.Bytes()

	blockNo += StakingDelay
	//staking 1+2 = 3
	//balance 2-2 = 0
	_, err = ExecuteSystemTx(scs, tx.GetBody(), sender, receiver, blockNo)
	assert.NoError(t, err, "Execute system tx failed in staking")
	assert.Equal(t, big.NewInt(0), sender.Balance(), "sender.Balance() should be 0 after staking")
	staking, err = getStaking(scs, tx.GetBody().GetAccount())
	assert.Equal(t, balance3, new(big.Int).SetBytes(staking.Amount), "check amount of staking")
	assert.Equal(t, balance3, receiver.Balance(), "check amount of staking")

	//voting still 1
	voteResult, err = getVoteResult(scs, defaultVoteKey, 1)
	assert.NoError(t, err, "get vote result")
	assert.Equal(t, types.StakingMinimum, new(big.Int).SetBytes(voteResult.Votes[0].Amount), "")

	tx.Body.Payload = []byte(`{"Name":"v1unstake"}`)
	tx.Body.Amount = types.StakingMinimum.Bytes()
	blockNo += StakingDelay
	//unstaking 3-1 = 2
	//balance 0+1 = 1
	//voting still 1
	_, err = ExecuteSystemTx(scs, tx.GetBody(), sender, receiver, blockNo)
	assert.NoError(t, err, "Execute system tx failed in unstaking")
	assert.Equal(t, types.StakingMinimum, new(big.Int).SetBytes(sender.Balance().Bytes()), "sender.Balance() should be turn back")
	staking, err = getStaking(scs, tx.GetBody().GetAccount())
	assert.Equal(t, balance2, new(big.Int).SetBytes(staking.Amount), "check amount of staking")
	assert.Equal(t, balance2, receiver.Balance(), "check amount of staking")
	voteResult, err = getVoteResult(scs, defaultVoteKey, 1)
	assert.NoError(t, err, "get vote reulst")
	assert.Equal(t, types.StakingMinimum, new(big.Int).SetBytes(voteResult.Votes[0].Amount), "")

	//unstaking 2-3 = -1(fail)
	//balance 1
	//voting 1
	tx.Body.Amount = balance3.Bytes()
	blockNo += StakingDelay
	_, err = ExecuteSystemTx(scs, tx.GetBody(), sender, receiver, blockNo)
	assert.EqualError(t, types.ErrExceedAmount, err.Error(), "should return exceed error")
	assert.Equal(t, types.StakingMinimum, new(big.Int).SetBytes(sender.Balance().Bytes()), "sender.Balance() should be turn back")
	staking, err = getStaking(scs, tx.GetBody().GetAccount())
	assert.Equal(t, balance2, new(big.Int).SetBytes(staking.Amount), "check amount of staking")
	voteResult, err = getVoteResult(scs, defaultVoteKey, 1)
	assert.NoError(t, err, "get vote reulst")
	assert.Equal(t, types.StakingMinimum, new(big.Int).SetBytes(voteResult.Votes[0].Amount), "")

	tx.Body.Amount = balance2.Bytes()
	blockNo += StakingDelay
	//unstaking 2-2 = 0
	//balance 1+2 = 3
	//voting 0
	_, err = ExecuteSystemTx(scs, tx.GetBody(), sender, receiver, blockNo)
	assert.NoError(t, err, "Execute system tx failed in unstaking")
	assert.Equal(t, balance3, new(big.Int).SetBytes(sender.Balance().Bytes()), "sender.Balance() should be turn back")
	staking, err = getStaking(scs, tx.GetBody().GetAccount())
	assert.Equal(t, big.NewInt(0), new(big.Int).SetBytes(staking.Amount), "check amount of staking")
	voteResult, err = getVoteResult(scs, defaultVoteKey, 1)
	assert.NoError(t, err, "get vote reulst")
	assert.Equal(t, big.NewInt(0), new(big.Int).SetBytes(voteResult.Votes[0].Amount), "")
}

func TestBasicFailedExecute(t *testing.T) {
	scs, sender, receiver := initTest(t)
	defer deinitTest()

	tx := &types.Tx{
		Body: &types.TxBody{
			Account:   sender.ID(),
			Recipient: []byte(types.AergoSystem),
			Amount:    types.StakingMinimum.Bytes(),
			Payload:   buildStakingPayload(false),
		},
	}
	senderBalance := big.NewInt(0).Add(types.StakingMinimum, types.StakingMinimum)
	sender.AddBalance(senderBalance)

	emptytx := &types.TxBody{}
	_, err := ExecuteSystemTx(scs, emptytx, sender, receiver, 0)
	assert.EqualError(t, types.ErrTxInvalidPayload, err.Error(), "should error")

	//staking 0+1 = 1
	//balance 2-1 = 1
	_, err = ExecuteSystemTx(scs, tx.GetBody(), sender, receiver, 0)
	assert.Error(t, err, "Execute system tx failed in unstaking")
	assert.Equal(t, sender.Balance(), senderBalance, "sender.Balance() should not chagned after failed unstaking")

	tx.Body.Payload = buildStakingPayload(true)
	_, err = ExecuteSystemTx(scs, tx.GetBody(), sender, receiver, 0)
	assert.NoError(t, err, "Execute system tx failed in staking")
	assert.Equal(t, sender.Balance(), types.StakingMinimum, "sender.Balance() should be 0 after staking")
	staking, err := getStaking(scs, tx.GetBody().GetAccount())
	assert.Equal(t, types.StakingMinimum, new(big.Int).SetBytes(staking.Amount), "check amount of staking")

	_, err = ExecuteSystemTx(scs, tx.GetBody(), sender, receiver, StakingDelay-1)
	assert.EqualError(t, types.ErrLessTimeHasPassed, err.Error(), "check staking delay")

	tx.Body.Payload = buildVotingPayload(1)
	_, err = ExecuteSystemTx(scs, tx.GetBody(), sender, receiver, VotingDelay)
	assert.NoError(t, err, "Execute system tx failed in voting")
	result, err := getVoteResult(scs, defaultVoteKey, 1)
	assert.Equal(t, types.StakingMinimum, result.Votes[0].GetAmountBigInt(), "check vote result")
	tx.Body.Payload = buildStakingPayload(false)
	tx.Body.Amount = senderBalance.Bytes()
	//staking 1-2 = -1 (fail)
	//balance still 1
	_, err = ExecuteSystemTx(scs, tx.GetBody(), sender, receiver, VotingDelay+StakingDelay)
	assert.Error(t, err, "should failed with exceed error")
	assert.Equal(t, types.StakingMinimum, sender.Balance(),
		"sender.Balance() should be turn back")
	staking, err = getStaking(scs, tx.GetBody().GetAccount())
	assert.Equal(t, types.StakingMinimum, new(big.Int).SetBytes(staking.Amount), "check amount of staking")

	//staking 1-1 = 0
	//balance 1+1 = 2
	tx.Body.Amount = types.StakingMinimum.Bytes()
	_, err = ExecuteSystemTx(scs, tx.GetBody(), sender, receiver, VotingDelay+StakingDelay)
	assert.NoError(t, err, "Execute system tx failed in staking")
	staking, err = getStaking(scs, tx.GetBody().GetAccount())
	assert.Equal(t, senderBalance, sender.Balance(),
		"sender.Balance() should be turn back")
	assert.Equal(t, big.NewInt(0), new(big.Int).SetBytes(staking.Amount), "check amount of staking")

	//staking 0-1 = -1 (fail)
	//balance still 2
	_, err = ExecuteSystemTx(scs, tx.GetBody(), sender, receiver, VotingDelay+StakingDelay)
	assert.EqualError(t, types.ErrMustStakeBeforeUnstake, err.Error(), "Execute system tx failed in unstaking")
}

func TestValidateSystemTxForStaking(t *testing.T) {
	scs, sender, receiver := initTest(t)
	defer deinitTest()

	scs, err := cdb.GetStateDB().OpenContractStateAccount(types.ToAccountID([]byte("aergo.system")))
	assert.NoError(t, err, "could not open contract state")

	tx := &types.Tx{
		Body: &types.TxBody{
			Account:   sender.ID(),
			Recipient: receiver.ID(),
			Amount:    types.StakingMinimum.Bytes(),
			Payload:   buildStakingPayload(true),
		},
	}
	sender.AddBalance(types.StakingMinimum)
	_, err = ValidateSystemTx(tx.Body.Account, tx.GetBody(), sender, scs, 0)
	assert.NoError(t, err, "Validate system tx failed")
	tx.Body.Amount = new(big.Int).Sub(types.StakingMinimum, new(big.Int).SetUint64(1)).Bytes()
}

func TestValidateSystemTxForUnstaking(t *testing.T) {
	scs, sender, receiver := initTest(t)
	defer deinitTest()
	const testSender = "AmPNYHyzyh9zweLwDyuoiUuTVCdrdksxkRWDjVJS76WQLExa2Jr4"

	scs, err := cdb.GetStateDB().OpenContractStateAccount(types.ToAccountID([]byte("aergo.system")))
	assert.NoError(t, err, "could not open contract state")

	account, err := types.DecodeAddress(testSender)
	assert.NoError(t, err, "could not decode test address")
	tx := &types.Tx{
		Body: &types.TxBody{
			Account: account,
			Amount:  types.StakingMinimum.Bytes(),
			Payload: buildStakingPayload(false),
		},
	}
	_, err = ValidateSystemTx(tx.Body.Account, tx.GetBody(), nil, scs, 0)
	assert.EqualError(t, types.ErrMustStakeBeforeUnstake, err.Error(), "Validate system tx failed")
	tx.Body.Amount = new(big.Int).Sub(types.StakingMinimum, new(big.Int).SetUint64(1)).Bytes()
	//_, err = ValidateSystemTx(tx.Body.Account, tx.GetBody(), nil, scs, 0)
	//assert.EqualError(t, err, types.ErrMustStakeBeforeUnstake.Error(), "Validate system tx failed")

	stakingTx := &types.Tx{
		Body: &types.TxBody{
			Account: account,
			Amount:  types.StakingMinimum.Bytes(),
			Payload: buildStakingPayload(true),
			Type:    types.TxType_GOVERNANCE,
		},
	}
	sender.AddBalance(types.StakingMinimum)

	_, err = ExecuteSystemTx(scs, stakingTx.GetBody(), sender, receiver, 0)
	assert.NoError(t, err, "could not execute system tx")

	tx.Body.Amount = types.StakingMinimum.Bytes()
	_, err = ValidateSystemTx(tx.Body.Account, tx.GetBody(), nil, scs, StakingDelay-1)
	assert.EqualError(t, types.ErrLessTimeHasPassed, err.Error(), "Validate system tx failed")
	_, err = ValidateSystemTx(tx.Body.Account, tx.GetBody(), nil, scs, StakingDelay)
	assert.NoError(t, err, "failed to validate system tx for unstaking")
}

func TestValidateSystemTxForVoting(t *testing.T) {
	scs, sender, receiver := initTest(t)
	defer deinitTest()
	const testSender = "AmPNYHyzyh9zweLwDyuoiUuTVCdrdksxkRWDjVJS76WQLExa2Jr4"
	const testCandidate = "16Uiu2HAmUJhjwotQqm7eGyZh1ZHrVviQJrdm2roQouD329vxZEkx"
	candidates, err := base58.Decode(testCandidate)
	assert.NoError(t, err, "could not decode candidates")

	account, err := types.DecodeAddress(testSender)
	assert.NoError(t, err, "could not decode test address")

	tx := &types.Tx{
		Body: &types.TxBody{
			Account: account,
			Payload: buildVotingPayload(0),
			Type:    types.TxType_GOVERNANCE,
		},
	}
	_, err = ValidateSystemTx(tx.Body.Account, tx.GetBody(), nil, scs, 1)
	assert.EqualError(t, err, types.ErrMustStakeBeforeVote.Error(), "Execute system tx failed")
	tx.Body.Payload = append(tx.Body.Payload, candidates...)

	sender.AddBalance(types.StakingMinimum)

	stakingTx := &types.Tx{
		Body: &types.TxBody{
			Account: account,
			Amount:  types.StakingMinimum.Bytes(),
			Payload: buildStakingPayload(true),
			Type:    types.TxType_GOVERNANCE,
		},
	}

	unStakingTx := &types.Tx{
		Body: &types.TxBody{
			Account: account,
			Amount:  types.StakingMinimum.Bytes(),
			Payload: buildStakingPayload(false),
			Type:    types.TxType_GOVERNANCE,
		},
	}
	var blockNo uint64
	blockNo = 1
	_, err = ExecuteSystemTx(scs, stakingTx.GetBody(), sender, receiver, blockNo)
	assert.NoError(t, err, "could not execute system tx")

	blockNo += StakingDelay
	_, err = ExecuteSystemTx(scs, stakingTx.GetBody(), sender, receiver, blockNo)
	assert.EqualError(t, err, types.ErrInsufficientBalance.Error(), "2nd staking tx")

	_, err = ValidateSystemTx(tx.Body.Account, tx.GetBody(), nil, scs, blockNo)
	assert.Error(t, err, "empty vote should not allowed")

	tx.Body.Payload = buildVotingPayload(10)
	_, err = ValidateSystemTx(tx.Body.Account, tx.GetBody(), nil, scs, blockNo)
	assert.NoError(t, err, "fisrt voting validation should success")

	_, err = ExecuteSystemTx(scs, tx.GetBody(), sender, receiver, blockNo)
	assert.NoError(t, err, "fisrt voting execution should success")

	blockNo++
	_, err = ValidateSystemTx(tx.Body.Account, tx.GetBody(), nil, scs, blockNo)
	assert.Error(t, err, "not enough delay, voting should fail")

	blockNo += VotingDelay
	_, err = ValidateSystemTx(tx.Body.Account, tx.GetBody(), nil, scs, blockNo)
	assert.NoError(t, err, "after delay, voting should success")

	tx.Body.Payload[1] = '2'
	_, err = ValidateSystemTx(tx.Body.Account, tx.GetBody(), nil, scs, blockNo)
	t.Log(err.Error())
	assert.NotNil(t, err, "failed to validate system tx for voting")

	tx.Body.Payload = append(tx.Body.Payload, 'i')
	_, err = ValidateSystemTx(tx.Body.Account, tx.GetBody(), nil, scs, blockNo)
	assert.EqualError(t, types.ErrTxInvalidPayload, err.Error(), "failed to validate system tx for voting")

	blockNo += StakingDelay
	_, err = ExecuteSystemTx(scs, unStakingTx.GetBody(), sender, receiver, blockNo)
	assert.NoError(t, err, "should execute unstaking system tx")
}

func TestRemainStakingMinimum(t *testing.T) {
	scs, sender, receiver := initTest(t)
	defer deinitTest()

	balance0_5 := new(big.Int).Div(types.StakingMinimum, big.NewInt(2))
	balance1 := types.StakingMinimum
	balance1_5 := new(big.Int).Add(balance1, balance0_5)
	balance2 := new(big.Int).Mul(balance1, big.NewInt(2))
	balance3 := new(big.Int).Mul(balance1, big.NewInt(3))
	sender.AddBalance(balance3)

	stakingTx := &types.Tx{
		Body: &types.TxBody{
			Account: sender.ID(),
			Payload: buildStakingPayload(true),
			Type:    types.TxType_GOVERNANCE,
		},
	}

	var blockNo uint64
	blockNo = 1
	stakingTx.Body.Amount = balance0_5.Bytes()
	_, err := ExecuteSystemTx(scs, stakingTx.GetBody(), sender, receiver, blockNo)
	assert.EqualError(t, err, types.ErrTooSmallAmount.Error(), "could not execute system tx")
	//balance 3-1.5=1.5
	//staking 0+1.5=1.5
	stakingTx.Body.Amount = balance1_5.Bytes()
	_, err = ExecuteSystemTx(scs, stakingTx.GetBody(), sender, receiver, blockNo)
	assert.NoError(t, err, "could not execute system tx")

	blockNo += StakingDelay
	stakingTx.Body.Amount = balance0_5.Bytes()
	//balance 1.5-0.5=1
	//staking 1.5+1.5=3
	_, err = ExecuteSystemTx(scs, stakingTx.GetBody(), sender, receiver, blockNo)
	assert.NoError(t, err, "could not execute system tx")

	stakingTx.Body.Amount = balance2.Bytes()
	//balance 1-2=-1 (fail)
	_, err = ExecuteSystemTx(scs, stakingTx.GetBody(), sender, receiver, blockNo+1)
	assert.EqualError(t, err, types.ErrInsufficientBalance.Error(), "check error")

	stakingTx.Body.Amount = balance1.Bytes()
	//time fail
	_, err = ExecuteSystemTx(scs, stakingTx.GetBody(), sender, receiver, blockNo+1)
	assert.EqualError(t, err, types.ErrLessTimeHasPassed.Error(), "check error")

	unStakingTx := &types.Tx{
		Body: &types.TxBody{
			Account: sender.ID(),
			Amount:  balance0_5.Bytes(),
			Payload: buildStakingPayload(false),
			Type:    types.TxType_GOVERNANCE,
		},
	}
	blockNo += StakingDelay - 1
	_, err = ExecuteSystemTx(scs, unStakingTx.GetBody(), sender, receiver, blockNo)
	assert.EqualError(t, err, types.ErrLessTimeHasPassed.Error(), "check error")

	blockNo += 1
	//balance 1+0.5 =1.5
	//staking 2-0.5 =1.5
	_, err = ExecuteSystemTx(scs, unStakingTx.GetBody(), sender, receiver, blockNo)
	assert.NoError(t, err, "could not execute system tx")
	staked, err := getStaking(scs, sender.ID())
	assert.NoError(t, err, "could not get staking")
	assert.Equal(t, balance1_5, sender.Balance(), "could not get staking")
	assert.Equal(t, balance1_5, staked.GetAmountBigInt(), "could not get staking")

	blockNo += StakingDelay
	//balance 1.5+0.5 =2
	//staking 1.5-0.5 =1
	_, err = ExecuteSystemTx(scs, unStakingTx.GetBody(), sender, receiver, blockNo)
	assert.NoError(t, err, "could not execute system tx")
	staked, err = getStaking(scs, sender.ID())
	assert.NoError(t, err, "could not get staking")
	assert.Equal(t, balance2, sender.Balance(), "could not get staking")
	assert.Equal(t, balance1, staked.GetAmountBigInt(), "could not get staking")

	blockNo += StakingDelay
	//staking 1-0.5 =0.5 (fail)
	_, err = ExecuteSystemTx(scs, unStakingTx.GetBody(), sender, receiver, blockNo)
	assert.EqualError(t, err, types.ErrTooSmallAmount.Error(), "staked aergo remain 0.5")
	staked, err = getStaking(scs, sender.ID())
	assert.NoError(t, err, "could not get staking")
	assert.Equal(t, balance2, sender.Balance(), "could not get staking")
	assert.Equal(t, balance1, staked.GetAmountBigInt(), "could not get staking")

	blockNo += StakingDelay
	unStakingTx.Body.Amount = balance1.Bytes()
	//balance 2+1 =3
	//staking 1-1 =0
	_, err = ExecuteSystemTx(scs, unStakingTx.GetBody(), sender, receiver, blockNo)
	assert.NoError(t, err, "could not execute system tx")
	staked, err = getStaking(scs, sender.ID())
	assert.NoError(t, err, "could not get staking")
	assert.Equal(t, balance3, sender.Balance(), "could not get staking")
	assert.Equal(t, big.NewInt(0), staked.GetAmountBigInt(), "could not get staking")

	_, err = ExecuteSystemTx(scs, unStakingTx.GetBody(), sender, receiver, blockNo)
	assert.EqualError(t, err, types.ErrMustStakeBeforeUnstake.Error(), "check error")
}

/*
func TestValidateVoteNumpBP(t *testing.T) {
scs,sender,receiver:=initTest(t)
	defer deinitTest()
	const testSender = "AmPNYHyzyh9zweLwDyuoiUuTVCdrdksxkRWDjVJS76WQLExa2Jr4"

	scs, err := cdb.GetStateDB().OpenContractStateAccount(types.ToAccountID([]byte("aergo.system")))
	assert.NoError(t, err, "could not open contract state")

	account, err := types.DecodeAddress(testSender)
	assert.NoError(t, err, "could not decode test address")
	tx := &types.Tx{
		Body: &types.TxBody{
			Account: account,
			Amount:  types.StakingMinimum.Bytes(),
			Payload: buildStakingPayload(true),
		},
	}
	sender, err := sdb.GetAccountStateV(tx.Body.Account)
	assert.NoError(t, err, "could not get test address state")
	receiver, err := sdb.GetAccountStateV(tx.Body.Recipient)
	assert.NoError(t, err, "could not get test address state")
	sender.AddBalance(types.StakingMinimum)

	_, err = ExecuteSystemTx(scs, tx.GetBody(), sender, receiver, 0)
	assert.NoError(t, err, "Execute system tx failed in staking")

	tx.Body.Payload = buildVotingPayloadEx(1, types.VoteBP)
	_, err = ExecuteSystemTx(scs, tx.GetBody(), sender, receiver, 1)
	assert.NoError(t, err, "Execute system tx failed in voting")
	tx.Body.Payload = buildVotingPayloadEx(1, types.VoteNumBP)
	_, err = ExecuteSystemTx(scs, tx.GetBody(), sender, receiver, 2)
	assert.NoError(t, err, "Execute system tx failed in voting")
}
*/
