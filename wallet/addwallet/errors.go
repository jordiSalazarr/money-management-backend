package addwallet

import "errors"

var (
	ErrMaxWalletsReached = errors.New("user cannot have more than 10 wallets")
)
