package voice

import (
	"context"
	"strings"

	dprojections "github.com/jordisalazar/money-management/debt/projections"
	rmprojections "github.com/jordisalazar/money-management/recurringmovement/projections"
	wprojections "github.com/jordisalazar/money-management/wallet/projections"
)

// Resolver translates human-readable names to entity IDs using projection stores.
type Resolver struct {
	walletStore    wprojections.WalletStore
	debtStore      dprojections.DebtStore
	recurringStore rmprojections.RecurringMovementStore
}

func NewResolver(
	walletStore wprojections.WalletStore,
	debtStore dprojections.DebtStore,
	recurringStore rmprojections.RecurringMovementStore,
) *Resolver {
	return &Resolver{
		walletStore:    walletStore,
		debtStore:      debtStore,
		recurringStore: recurringStore,
	}
}

// WalletResult holds the resolution result for a wallet lookup.
type WalletResult struct {
	Wallet   wprojections.WalletView
	Options  []string // populated when ambiguous
	NotFound bool
}

// ResolveWallet finds a wallet by name for a given user.
func (r *Resolver) ResolveWallet(ctx context.Context, userID, name string) (*WalletResult, error) {
	wallets, err := r.walletStore.ListByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}

	needle := strings.ToLower(strings.TrimSpace(name))

	// Exact match first.
	for _, w := range wallets {
		if strings.ToLower(w.Name) == needle {
			return &WalletResult{Wallet: w}, nil
		}
	}

	// Partial match.
	var matches []wprojections.WalletView
	for _, w := range wallets {
		if strings.Contains(strings.ToLower(w.Name), needle) {
			matches = append(matches, w)
		}
	}

	if len(matches) == 1 {
		return &WalletResult{Wallet: matches[0]}, nil
	}
	if len(matches) > 1 {
		options := make([]string, len(matches))
		for i, m := range matches {
			options[i] = m.Name
		}
		return &WalletResult{Options: options}, nil
	}

	return &WalletResult{NotFound: true}, nil
}

// DebtResult holds the resolution result for a debt lookup.
type DebtResult struct {
	Debt     dprojections.DebtView
	Options  []string
	NotFound bool
}

// ResolveDebt finds an unsettled debt by reason text for a given user.
func (r *Resolver) ResolveDebt(ctx context.Context, userID, reason string, amount int) (*DebtResult, error) {
	status := "created"
	debts, err := r.debtStore.List(ctx, dprojections.DebtFilter{
		OwnerID: userID,
		Status:  &status,
	})
	if err != nil {
		return nil, err
	}

	needle := strings.ToLower(strings.TrimSpace(reason))

	var matches []dprojections.DebtView
	for _, d := range debts {
		reasonMatch := strings.Contains(strings.ToLower(d.Reason), needle)
		amountMatch := amount == 0 || d.Amount == amount
		if reasonMatch && amountMatch {
			matches = append(matches, d)
		}
	}

	if len(matches) == 1 {
		return &DebtResult{Debt: matches[0]}, nil
	}
	if len(matches) > 1 {
		options := make([]string, len(matches))
		for i, m := range matches {
			options[i] = m.Reason
		}
		return &DebtResult{Options: options}, nil
	}

	return &DebtResult{NotFound: true}, nil
}

// ResolveRecurringMovement finds an active recurring movement by description.
func (r *Resolver) ResolveRecurringMovement(ctx context.Context, userID, reason, walletName string) (string, []string, error) {
	// First resolve wallet if provided for filtering.
	var walletID string
	if walletName != "" {
		result, err := r.ResolveWallet(ctx, userID, walletName)
		if err != nil {
			return "", nil, err
		}
		if !result.NotFound && len(result.Options) == 0 {
			walletID = result.Wallet.ID
		}
	}

	active := true
	filter := rmprojections.RecurringMovementFilter{Active: &active}
	if walletID != "" {
		filter.WalletID = walletID
	}

	movements, err := r.recurringStore.List(ctx, filter)
	if err != nil {
		return "", nil, err
	}

	// Filter by user and reason.
	needle := strings.ToLower(strings.TrimSpace(reason))
	var matches []rmprojections.RecurringMovementView
	for _, m := range movements {
		if m.UserID != userID {
			continue
		}
		if strings.Contains(strings.ToLower(m.Reason), needle) {
			matches = append(matches, m)
		}
	}

	if len(matches) == 1 {
		return matches[0].ID, nil, nil
	}
	if len(matches) > 1 {
		options := make([]string, len(matches))
		for i, m := range matches {
			options[i] = m.Reason
		}
		return "", options, nil
	}

	return "", nil, nil // not found
}
