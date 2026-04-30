package voice

import "encoding/json"

// Tool is a Claude tool definition.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// AllTools returns the 8 tool definitions for all supported voice commands.
func AllTools() []Tool {
	return []Tool{
		{
			Name:        "create_wallet",
			Description: "Create a new wallet. Example: 'create a wallet called Savings'",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"name":           {"type": "string", "description": "Wallet name"},
					"description":    {"type": "string", "description": "Optional wallet description"},
					"initial_amount": {"type": "integer", "description": "Initial balance in cents. Default 0"},
					"currency":       {"type": "string", "enum": ["EUR", "USD"], "description": "Currency. Default EUR"},
					"category":       {"type": "string", "enum": ["Cash", "Saving", "Investment", "Food", "Transport", "Entertainment", "Other"], "description": "Wallet category. Default Other"}
				},
				"required": ["name"]
			}`),
		},
		{
			Name:        "deposit_money",
			Description: "Add money to a wallet. Example: 'add 100 euros to savings'",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"wallet_name": {"type": "string", "description": "Name of the wallet to deposit into"},
					"amount":      {"type": "integer", "description": "Amount in cents"},
					"currency":    {"type": "string", "enum": ["EUR", "USD"], "description": "Currency. Default EUR"},
					"reason":      {"type": "string", "description": "Reason for the deposit"}
				},
				"required": ["wallet_name", "amount"]
			}`),
		},
		{
			Name:        "withdraw_money",
			Description: "Take money out of a wallet. Example: 'withdraw 30 euros from groceries for lunch'",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"wallet_name": {"type": "string", "description": "Name of the wallet to withdraw from"},
					"amount":      {"type": "integer", "description": "Amount in cents"},
					"currency":    {"type": "string", "enum": ["EUR", "USD"], "description": "Currency. Default EUR"},
					"reason":      {"type": "string", "description": "Reason for the withdrawal"},
					"category":    {"type": "string", "enum": ["Cash", "Saving", "Investment", "Food", "Transport", "Entertainment", "Other"], "description": "Expense category. Default Other"}
				},
				"required": ["wallet_name", "amount"]
			}`),
		},
		{
			Name:        "transfer_money",
			Description: "Move money between two wallets. Example: 'move 50 euros from savings to groceries'",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"from_wallet_name": {"type": "string", "description": "Source wallet name"},
					"to_wallet_name":   {"type": "string", "description": "Destination wallet name"},
					"amount":           {"type": "integer", "description": "Amount in cents"},
					"currency":         {"type": "string", "enum": ["EUR", "USD"], "description": "Currency. Default EUR"},
					"reason":           {"type": "string", "description": "Reason for the transfer"}
				},
				"required": ["from_wallet_name", "to_wallet_name", "amount"]
			}`),
		},
		{
			Name:        "record_debt",
			Description: "Record a debt. Example: 'I owe John 20 euros for dinner' or 'Maria owes me 15 euros'",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"wallet_name": {"type": "string", "description": "Optional wallet to associate the debt with"},
					"amount":      {"type": "integer", "description": "Amount in cents"},
					"currency":    {"type": "string", "enum": ["EUR", "USD"], "description": "Currency. Default EUR"},
					"direction":   {"type": "string", "enum": ["IN", "OUT"], "description": "IN = someone owes me, OUT = I owe someone"},
					"reason":      {"type": "string", "description": "Who and why, e.g. 'John for dinner'"}
				},
				"required": ["amount", "direction", "reason"]
			}`),
		},
		{
			Name:        "settle_debt",
			Description: "Settle/pay off an existing debt. Example: 'settle the dinner debt with John'",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"debt_reason": {"type": "string", "description": "Description to identify the debt (reason or person name)"},
					"debt_amount": {"type": "integer", "description": "Optional amount in cents to help identify the debt"}
				},
				"required": ["debt_reason"]
			}`),
		},
		{
			Name:        "create_recurring_movement",
			Description: "Set up a recurring deposit or withdrawal. Example: 'every month deposit 500 euros into savings for salary'",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"wallet_name": {"type": "string", "description": "Wallet name for the recurring movement"},
					"type":        {"type": "string", "enum": ["deposit", "withdrawal"], "description": "Type of movement"},
					"amount":      {"type": "integer", "description": "Amount in cents"},
					"currency":    {"type": "string", "enum": ["EUR", "USD"], "description": "Currency. Default EUR"},
					"frequency":   {"type": "string", "enum": ["DAILY", "WEEKLY", "MONTHLY"], "description": "How often"},
					"reason":      {"type": "string", "description": "Reason for the recurring movement"},
					"start_date":  {"type": "string", "description": "Start date in YYYY-MM-DD format. Default today"},
					"end_date":    {"type": "string", "description": "Optional end date in YYYY-MM-DD format"}
				},
				"required": ["wallet_name", "type", "amount", "frequency"]
			}`),
		},
		{
			Name:        "stop_recurring_movement",
			Description: "Stop a recurring movement. Example: 'stop the monthly salary deposit'",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"recurring_reason": {"type": "string", "description": "Description to identify the recurring movement (reason or wallet name)"},
					"wallet_name":      {"type": "string", "description": "Optional wallet name to help identify"},
					"stop_reason":      {"type": "string", "description": "Why it's being stopped"}
				},
				"required": ["recurring_reason"]
			}`),
		},
	}
}
