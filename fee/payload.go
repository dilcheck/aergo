package fee

import (
	"math/big"
)

const (
	baseTxFee            = "2000000000000000" // 0.002 AERGO
	aerPerByte           = 5000000000000      // 5,000 GAER, feePerBytes * PayloadMaxBytes = 1 AERGO
	payloadMaxSize       = 200 * 1024
	StateDbMaxUpdateSize = payloadMaxSize
	freeByteSize         = 200
)

var (
	baseTxAergo   *big.Int
	zeroFee       bool
	stateDbMaxFee *big.Int
	zero          *big.Int
	AerPerByte    *big.Int
)

func init() {
	baseTxAergo, _ = new(big.Int).SetString(baseTxFee, 10)
	zeroFee = false
	AerPerByte = big.NewInt(aerPerByte)
	stateDbMaxFee = new(big.Int).Mul(AerPerByte, big.NewInt(StateDbMaxUpdateSize-freeByteSize))
	zero = big.NewInt(0)
}

func EnableZeroFee() {
	zeroFee = true
}

func IsZeroFee() bool {
	return zeroFee
}

func PayloadTxFee(payloadSize int) *big.Int {
	if IsZeroFee() {
		return zero
	}
	size := PaymentDataSize(int64(payloadSize))
	if size > payloadMaxSize {
		size = payloadMaxSize
	}
	return new(big.Int).Add(
		baseTxAergo,
		new(big.Int).Mul(
			AerPerByte,
			big.NewInt(size),
		),
	)
}

func MaxPayloadTxFee(payloadSize int) *big.Int {
	if IsZeroFee() {
		return zero
	}
	if payloadSize == 0 {
		return baseTxAergo
	}
	return new(big.Int).Add(PayloadTxFee(payloadSize), stateDbMaxFee)
}

func PaymentDataSize(dataSize int64) int64 {
	pSize := dataSize - freeByteSize
	if pSize < 0 {
		pSize = 0
	}
	return pSize
}
