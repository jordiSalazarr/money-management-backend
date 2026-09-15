package refundmoney

// This slice deliberately has no handler.go and no route.
//
// Every other command slice exposes an HTTP endpoint; this one does not,
// because a refund is never something a person asks for. It is the
// compensating leg a process runs when a later step has already failed, so its
// only caller is a reactor. Exposing it would let a caller mint money into a
// wallet while bypassing the disabled-wallet and category checks that
// depositmoney enforces.
//
// There is therefore no RegisterCommands either — processes construct the
// command handler directly. See ADR 0002.
