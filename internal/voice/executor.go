package voice

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jordisalazar/money-management/internal/payments/debt/slices/recorddebt"
	"github.com/jordisalazar/money-management/internal/payments/debt/slices/settledebt"
	"github.com/jordisalazar/money-management/internal/payments/processes/transfer/slices/requesttransfer"
	"github.com/jordisalazar/money-management/internal/payments/recurringmovement/slices/recordrecurringmovement"
	"github.com/jordisalazar/money-management/internal/payments/recurringmovement/slices/stoprecurringmovement"
	"github.com/jordisalazar/money-management/internal/payments/wallet/slices/addwallet"
	"github.com/jordisalazar/money-management/internal/payments/wallet/slices/depositmoney"
	"github.com/jordisalazar/money-management/internal/payments/wallet/slices/withdrawmoney"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

// Executor routes a Claude tool call to the appropriate command handler.
type Executor struct {
	store    eventstore.Store
	codec    *eventstore.Codec
	resolver *Resolver
}

func NewExecutor(store eventstore.Store, codec *eventstore.Codec, resolver *Resolver) *Executor {
	return &Executor{store: store, codec: codec, resolver: resolver}
}

// ExecuteResult is returned after executing a command.
type ExecuteResult struct {
	Success       bool     `json:"success"`
	Message       string   `json:"message"`
	NeedsClarify  bool     `json:"needs_clarify,omitempty"`
	Options       []string `json:"options,omitempty"`
	ClarifyPrompt string   `json:"clarify_prompt,omitempty"`
}

// Execute runs the appropriate command based on the tool name and input.
func (e *Executor) Execute(ctx context.Context, userID, toolName string, input json.RawMessage) *ExecuteResult {
	switch toolName {
	case "create_wallet":
		return e.createWallet(ctx, userID, input)
	case "deposit_money":
		return e.depositMoney(ctx, userID, input)
	case "withdraw_money":
		return e.withdrawMoney(ctx, userID, input)
	case "transfer_money":
		return e.transferMoney(ctx, userID, input)
	case "record_debt":
		return e.recordDebt(ctx, userID, input)
	case "settle_debt":
		return e.settleDebt(ctx, userID, input)
	case "create_recurring_movement":
		return e.createRecurringMovement(ctx, userID, input)
	case "stop_recurring_movement":
		return e.stopRecurringMovement(ctx, userID, input)
	default:
		return &ExecuteResult{Message: fmt.Sprintf("Unknown command: %s", toolName)}
	}
}

// --- Tool input structs (what Claude sends) ---

type createWalletInput struct {
	Name          string `json:"name"`
	Description   string `json:"description"`
	InitialAmount int    `json:"initial_amount"`
	Currency      string `json:"currency"`
	Category      string `json:"category"`
}

type depositMoneyInput struct {
	WalletName string `json:"wallet_name"`
	Amount     int    `json:"amount"`
	Currency   string `json:"currency"`
	Reason     string `json:"reason"`
	Category   string `json:"category"`
}

type withdrawMoneyInput struct {
	WalletName string `json:"wallet_name"`
	Amount     int    `json:"amount"`
	Currency   string `json:"currency"`
	Reason     string `json:"reason"`
	Category   string `json:"category"`
}

type transferMoneyInput struct {
	FromWalletName string `json:"from_wallet_name"`
	ToWalletName   string `json:"to_wallet_name"`
	Amount         int    `json:"amount"`
	Currency       string `json:"currency"`
	Reason         string `json:"reason"`
}

type recordDebtInput struct {
	WalletName string `json:"wallet_name"`
	Amount     int    `json:"amount"`
	Currency   string `json:"currency"`
	Direction  string `json:"direction"`
	Reason     string `json:"reason"`
}

type settleDebtInput struct {
	DebtReason string `json:"debt_reason"`
	DebtAmount int    `json:"debt_amount"`
}

type createRecurringInput struct {
	WalletName string `json:"wallet_name"`
	Type       string `json:"type"`
	Amount     int    `json:"amount"`
	Currency   string `json:"currency"`
	Frequency  string `json:"frequency"`
	Reason     string `json:"reason"`
	StartDate  string `json:"start_date"`
	EndDate    string `json:"end_date"`
}

type stopRecurringInput struct {
	RecurringReason string `json:"recurring_reason"`
	WalletName      string `json:"wallet_name"`
	StopReason      string `json:"stop_reason"`
}

// --- Handlers ---

func (e *Executor) createWallet(ctx context.Context, userID string, input json.RawMessage) *ExecuteResult {
	var in createWalletInput
	if err := json.Unmarshal(input, &in); err != nil {
		slog.Error("voice: parse failed", "tool", "create_wallet", "err", err, "raw_input", string(input))
		return &ExecuteResult{Message: "Failed to parse wallet details"}
	}

	currency := defaultStr(in.Currency, "EUR")
	category := defaultStr(in.Category, "Other")

	cmd := addwallet.Command{
		UserID:        userID,
		Name:          in.Name,
		Description:   in.Description,
		InitialAmount: in.InitialAmount,
		Currency:      currency,
		Category:      category,
	}

	handler := addwallet.NewCommandHandler(e.store, e.codec)
	_, err := handler.Handle(ctx, cmd)
	if err != nil {
		return &ExecuteResult{Message: fmt.Sprintf("Failed to create wallet: %s", err.Error())}
	}

	return &ExecuteResult{
		Success: true,
		Message: fmt.Sprintf("Created wallet '%s' (%s)", in.Name, currency),
	}
}

func (e *Executor) depositMoney(ctx context.Context, userID string, input json.RawMessage) *ExecuteResult {
	var in depositMoneyInput
	if err := json.Unmarshal(input, &in); err != nil {
		slog.Error("voice: parse failed", "tool", "deposit_money", "err", err, "raw_input", string(input))
		return &ExecuteResult{Message: "Failed to parse deposit details"}
	}

	result, err := e.resolver.ResolveWallet(ctx, userID, in.WalletName)
	if err != nil {
		return &ExecuteResult{Message: "Failed to look up wallet"}
	}
	if result.NotFound {
		return &ExecuteResult{Message: fmt.Sprintf("No wallet found matching '%s'", in.WalletName)}
	}
	if len(result.Options) > 0 {
		return &ExecuteResult{
			NeedsClarify:  true,
			ClarifyPrompt: fmt.Sprintf("Multiple wallets match '%s'. Which one?", in.WalletName),
			Options:       result.Options,
		}
	}

	currency := defaultStr(in.Currency, result.Wallet.Currency)

	category := defaultStr(in.Category, "Other")

	cmd := depositmoney.Command{
		WalletID: result.Wallet.ID,
		UserID:   userID,
		Amount:   in.Amount,
		Currency: currency,
		Reason:   in.Reason,
		Category: category,
	}

	handler := depositmoney.NewCommandHandler(e.store, e.codec)
	_, err = handler.Handle(ctx, cmd)
	if err != nil {
		return &ExecuteResult{Message: fmt.Sprintf("Failed to deposit: %s", err.Error())}
	}

	return &ExecuteResult{
		Success: true,
		Message: fmt.Sprintf("Deposited %s to %s", formatCents(in.Amount, currency), result.Wallet.Name),
	}
}

func (e *Executor) withdrawMoney(ctx context.Context, userID string, input json.RawMessage) *ExecuteResult {
	var in withdrawMoneyInput
	if err := json.Unmarshal(input, &in); err != nil {
		slog.Error("voice: parse failed", "tool", "withdraw_money", "err", err, "raw_input", string(input))
		return &ExecuteResult{Message: "Failed to parse withdrawal details"}
	}

	result, err := e.resolver.ResolveWallet(ctx, userID, in.WalletName)
	if err != nil {
		return &ExecuteResult{Message: "Failed to look up wallet"}
	}
	if result.NotFound {
		return &ExecuteResult{Message: fmt.Sprintf("No wallet found matching '%s'", in.WalletName)}
	}
	if len(result.Options) > 0 {
		return &ExecuteResult{
			NeedsClarify:  true,
			ClarifyPrompt: fmt.Sprintf("Multiple wallets match '%s'. Which one?", in.WalletName),
			Options:       result.Options,
		}
	}

	currency := defaultStr(in.Currency, result.Wallet.Currency)
	category := defaultStr(in.Category, "Other")

	cmd := withdrawmoney.Command{
		WalletID: result.Wallet.ID,
		UserID:   userID,
		Amount:   in.Amount,
		Currency: currency,
		Reason:   in.Reason,
		Category: category,
	}

	handler := withdrawmoney.NewCommandHandler(e.store, e.codec)
	_, err = handler.Handle(ctx, cmd)
	if err != nil {
		return &ExecuteResult{Message: fmt.Sprintf("Failed to withdraw: %s", err.Error())}
	}

	return &ExecuteResult{
		Success: true,
		Message: fmt.Sprintf("Withdrew %s from %s", formatCents(in.Amount, currency), result.Wallet.Name),
	}
}

func (e *Executor) transferMoney(ctx context.Context, userID string, input json.RawMessage) *ExecuteResult {
	var in transferMoneyInput
	if err := json.Unmarshal(input, &in); err != nil {
		slog.Error("voice: parse failed", "tool", "transfer_money", "err", err, "raw_input", string(input))
		return &ExecuteResult{Message: "Failed to parse transfer details"}
	}

	fromResult, err := e.resolver.ResolveWallet(ctx, userID, in.FromWalletName)
	if err != nil {
		return &ExecuteResult{Message: "Failed to look up source wallet"}
	}
	if fromResult.NotFound {
		return &ExecuteResult{Message: fmt.Sprintf("No wallet found matching '%s'", in.FromWalletName)}
	}
	if len(fromResult.Options) > 0 {
		return &ExecuteResult{
			NeedsClarify:  true,
			ClarifyPrompt: fmt.Sprintf("Multiple wallets match '%s'. Which source wallet?", in.FromWalletName),
			Options:       fromResult.Options,
		}
	}

	toResult, err := e.resolver.ResolveWallet(ctx, userID, in.ToWalletName)
	if err != nil {
		return &ExecuteResult{Message: "Failed to look up destination wallet"}
	}
	if toResult.NotFound {
		return &ExecuteResult{Message: fmt.Sprintf("No wallet found matching '%s'", in.ToWalletName)}
	}
	if len(toResult.Options) > 0 {
		return &ExecuteResult{
			NeedsClarify:  true,
			ClarifyPrompt: fmt.Sprintf("Multiple wallets match '%s'. Which destination wallet?", in.ToWalletName),
			Options:       toResult.Options,
		}
	}

	currency := defaultStr(in.Currency, fromResult.Wallet.Currency)

	handler := requesttransfer.NewCommandHandler(e.store, e.codec)
	if _, err = handler.Handle(ctx, requesttransfer.Command{
		UserID:       userID,
		FromWalletID: fromResult.Wallet.ID,
		ToWalletID:   toResult.Wallet.ID,
		Amount:       in.Amount,
		Currency:     currency,
		Reason:       in.Reason,
	}); err != nil {
		return &ExecuteResult{Message: fmt.Sprintf("Failed to request transfer: %s", err.Error())}
	}

	// A transfer is requested, not performed: the reactor carries it out and
	// it can still fail. The wording has to be honest about that, because
	// telling someone their money has moved when it has only been accepted is
	// exactly the kind of lie this migration exists to remove.
	return &ExecuteResult{
		Success: true,
		Message: fmt.Sprintf("Started a transfer of %s from %s to %s — it will complete shortly",
			formatCents(in.Amount, currency), fromResult.Wallet.Name, toResult.Wallet.Name),
	}
}

func (e *Executor) recordDebt(ctx context.Context, userID string, input json.RawMessage) *ExecuteResult {
	var in recordDebtInput
	if err := json.Unmarshal(input, &in); err != nil {
		slog.Error("voice: parse failed", "tool", "record_debt", "err", err, "raw_input", string(input))
		return &ExecuteResult{Message: "Failed to parse debt details"}
	}

	currency := defaultStr(in.Currency, "EUR")

	var walletID string
	if in.WalletName != "" {
		result, err := e.resolver.ResolveWallet(ctx, userID, in.WalletName)
		if err != nil {
			return &ExecuteResult{Message: "Failed to look up wallet"}
		}
		if result.NotFound {
			return &ExecuteResult{Message: fmt.Sprintf("No wallet found matching '%s'", in.WalletName)}
		}
		if len(result.Options) > 0 {
			return &ExecuteResult{
				NeedsClarify:  true,
				ClarifyPrompt: fmt.Sprintf("Multiple wallets match '%s'. Which one?", in.WalletName),
				Options:       result.Options,
			}
		}
		walletID = result.Wallet.ID
	}

	cmd := recorddebt.Command{
		OwnerID:   userID,
		WalletID:  walletID,
		Amount:    in.Amount,
		Currency:  currency,
		Direction: in.Direction,
		Reason:    in.Reason,
	}

	handler := recorddebt.NewCommandHandler(e.store, e.codec)
	_, err := handler.Handle(ctx, cmd)
	if err != nil {
		return &ExecuteResult{Message: fmt.Sprintf("Failed to record debt: %s", err.Error())}
	}

	dir := "owed to you"
	if in.Direction == "OUT" {
		dir = "you owe"
	}
	return &ExecuteResult{
		Success: true,
		Message: fmt.Sprintf("Recorded debt: %s %s — %s", formatCents(in.Amount, currency), dir, in.Reason),
	}
}

func (e *Executor) settleDebt(ctx context.Context, userID string, input json.RawMessage) *ExecuteResult {
	var in settleDebtInput
	if err := json.Unmarshal(input, &in); err != nil {
		return &ExecuteResult{Message: "Failed to parse settle details"}
	}

	result, err := e.resolver.ResolveDebt(ctx, userID, in.DebtReason, in.DebtAmount)
	if err != nil {
		return &ExecuteResult{Message: "Failed to look up debt"}
	}
	if result.NotFound {
		return &ExecuteResult{Message: fmt.Sprintf("No unsettled debt found matching '%s'", in.DebtReason)}
	}
	if len(result.Options) > 0 {
		return &ExecuteResult{
			NeedsClarify:  true,
			ClarifyPrompt: fmt.Sprintf("Multiple debts match '%s'. Which one?", in.DebtReason),
			Options:       result.Options,
		}
	}

	cmd := settledebt.Command{
		DebtID:    result.Debt.ID,
		SettledBy: userID,
	}

	handler := settledebt.NewCommandHandler(e.store, e.codec)
	if err := handler.Handle(ctx, cmd); err != nil {
		return &ExecuteResult{Message: fmt.Sprintf("Failed to settle debt: %s", err.Error())}
	}

	return &ExecuteResult{
		Success: true,
		Message: fmt.Sprintf("Settled debt: %s — %s", formatCents(result.Debt.Amount, result.Debt.Currency), result.Debt.Reason),
	}
}

func (e *Executor) createRecurringMovement(ctx context.Context, userID string, input json.RawMessage) *ExecuteResult {
	var in createRecurringInput
	if err := json.Unmarshal(input, &in); err != nil {
		return &ExecuteResult{Message: "Failed to parse recurring movement details"}
	}

	result, err := e.resolver.ResolveWallet(ctx, userID, in.WalletName)
	if err != nil {
		return &ExecuteResult{Message: "Failed to look up wallet"}
	}
	if result.NotFound {
		return &ExecuteResult{Message: fmt.Sprintf("No wallet found matching '%s'", in.WalletName)}
	}
	if len(result.Options) > 0 {
		return &ExecuteResult{
			NeedsClarify:  true,
			ClarifyPrompt: fmt.Sprintf("Multiple wallets match '%s'. Which one?", in.WalletName),
			Options:       result.Options,
		}
	}

	currency := defaultStr(in.Currency, result.Wallet.Currency)

	now := time.Now().UTC()
	startDate := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if in.StartDate != "" {
		parsed, err := time.Parse("2006-01-02", in.StartDate)
		if err == nil {
			startDate = parsed
		}
	}

	var endDate time.Time
	if in.EndDate != "" {
		parsed, err := time.Parse("2006-01-02", in.EndDate)
		if err == nil {
			endDate = parsed
		}
	}

	cmd := recordrecurringmovement.Command{
		WalletID:  result.Wallet.ID,
		UserID:    userID,
		Type:      in.Type,
		Amount:    in.Amount,
		Currency:  currency,
		Frequency: in.Frequency,
		Reason:    in.Reason,
		StartDate: startDate,
		EndDate:   endDate,
	}

	handler := recordrecurringmovement.NewCommandHandler(e.store, e.codec)
	_, err = handler.Handle(ctx, cmd)
	if err != nil {
		return &ExecuteResult{Message: fmt.Sprintf("Failed to create recurring movement: %s", err.Error())}
	}

	return &ExecuteResult{
		Success: true,
		Message: fmt.Sprintf("Created %s recurring %s of %s on %s", in.Frequency, in.Type, formatCents(in.Amount, currency), result.Wallet.Name),
	}
}

func (e *Executor) stopRecurringMovement(ctx context.Context, userID string, input json.RawMessage) *ExecuteResult {
	var in stopRecurringInput
	if err := json.Unmarshal(input, &in); err != nil {
		return &ExecuteResult{Message: "Failed to parse stop details"}
	}

	id, options, err := e.resolver.ResolveRecurringMovement(ctx, userID, in.RecurringReason, in.WalletName)
	if err != nil {
		return &ExecuteResult{Message: "Failed to look up recurring movement"}
	}
	if id == "" && len(options) == 0 {
		return &ExecuteResult{Message: fmt.Sprintf("No active recurring movement found matching '%s'", in.RecurringReason)}
	}
	if len(options) > 0 {
		return &ExecuteResult{
			NeedsClarify:  true,
			ClarifyPrompt: fmt.Sprintf("Multiple recurring movements match '%s'. Which one?", in.RecurringReason),
			Options:       options,
		}
	}

	cmd := stoprecurringmovement.Command{
		RecurringMovementID: id,
		StoppedBy:           userID,
		Reason:              defaultStr(in.StopReason, "Stopped via voice command"),
	}

	handler := stoprecurringmovement.NewCommandHandler(e.store, e.codec)
	_, err = handler.Handle(ctx, cmd)
	if err != nil {
		return &ExecuteResult{Message: fmt.Sprintf("Failed to stop recurring movement: %s", err.Error())}
	}

	return &ExecuteResult{
		Success: true,
		Message: "Stopped recurring movement",
	}
}

// --- Helpers ---

func defaultStr(val, fallback string) string {
	if val == "" {
		return fallback
	}
	return val
}

func formatCents(cents int, currency string) string {
	symbol := "€"
	if currency == "USD" {
		symbol = "$"
	}
	return fmt.Sprintf("%s%d.%02d", symbol, cents/100, cents%100)
}
