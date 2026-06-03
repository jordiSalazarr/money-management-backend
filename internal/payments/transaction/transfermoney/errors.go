package transfermoney

import "errors"

var (
	ErrSameWallet = errors.New("source and destination wallet must be different")
)
