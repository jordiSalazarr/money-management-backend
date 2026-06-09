package voice

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	dprojections "github.com/jordisalazar/money-management/internal/payments/debt/projections"
	rmprojections "github.com/jordisalazar/money-management/internal/payments/recurringmovement/projections"
	"github.com/jordisalazar/money-management/internal/shared/middleware"
)

// Handler handles voice command HTTP requests.
type Handler struct {
	client   Backend
	executor *Executor
	resolver *Resolver
}

func NewHandler(client Backend, executor *Executor, resolver *Resolver) *Handler {
	return &Handler{client: client, executor: executor, resolver: resolver}
}

type voiceRequest struct {
	Text string `json:"text"`
}

type voiceResponse struct {
	Status  string   `json:"status"`
	Message string   `json:"message"`
	Options []string `json:"options,omitempty"`
}

// HandleVoiceCommand processes a voice command.
// @Summary Process a voice command
// @Description Interprets natural language and executes the corresponding action
// @Tags voice
// @Accept json
// @Produce json
// @Param request body voiceRequest true "Voice command"
// @Success 200 {object} voiceResponse
// @Failure 400 {object} voiceResponse
// @Failure 500 {object} voiceResponse
// @Security BearerAuth
// @Router /voice/command [post]
func (h *Handler) HandleVoiceCommand(c *gin.Context) {
	var req voiceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, voiceResponse{
			Status:  "error",
			Message: "Invalid request body",
		})
		return
	}

	if req.Text == "" {
		c.JSON(http.StatusBadRequest, voiceResponse{
			Status:  "error",
			Message: "Text is required",
		})
		return
	}

	userID := middleware.GetUserID(c.Request.Context())
	if userID == "" {
		c.JSON(http.StatusUnauthorized, voiceResponse{
			Status:  "error",
			Message: "Unauthorized",
		})
		return
	}

	slog.Info("voice command received", "user_id", userID, "text", req.Text)

	// Step 1: Build user context (wallet list, debts, etc.) for Claude.
	userContext := h.buildUserContext(c.Request.Context(), userID)

	// Step 2: Send to Claude for interpretation.
	tools := AllTools()
	result, err := h.client.Interpret(c.Request.Context(), req.Text, tools, userContext)
	if err != nil {
		slog.Error("claude interpretation failed", "error", err)
		c.JSON(http.StatusInternalServerError, voiceResponse{
			Status:  "error",
			Message: "Failed to interpret command. Please try again.",
		})
		return
	}

	// Step 3: If Claude returned text instead of a tool call, it couldn't understand.
	if result.Text != "" {
		c.JSON(http.StatusOK, voiceResponse{
			Status:  "error",
			Message: result.Text,
		})
		return
	}

	slog.Info("voice command interpreted", "user_id", userID, "tool", result.ToolName)

	// Step 4: Execute the command.
	execResult := h.executor.Execute(c.Request.Context(), userID, result.ToolName, result.ToolInput)

	if execResult.NeedsClarify {
		c.JSON(http.StatusOK, voiceResponse{
			Status:  "clarification_needed",
			Message: execResult.ClarifyPrompt,
			Options: execResult.Options,
		})
		return
	}

	if !execResult.Success {
		c.JSON(http.StatusOK, voiceResponse{
			Status:  "error",
			Message: execResult.Message,
		})
		return
	}

	c.JSON(http.StatusOK, voiceResponse{
		Status:  "success",
		Message: execResult.Message,
	})
}

// buildUserContext fetches the user's wallets, debts, and recurring movements
// and formats them as context for Claude so it can match spoken names to exact names.
func (h *Handler) buildUserContext(ctx context.Context, userID string) string {
	var parts []string

	// Wallets
	wallets, err := h.resolver.walletStore.ListByUserID(ctx, userID)
	if err == nil && len(wallets) > 0 {
		var lines []string
		for _, w := range wallets {
			lines = append(lines, fmt.Sprintf("- \"%s\" (%s, balance: %d cents)", w.Name, w.Currency, w.CurrentBalance))
		}
		parts = append(parts, "The user's wallets:\n"+strings.Join(lines, "\n"))
	}

	// Debts
	status := "created"
	debts, err := h.resolver.debtStore.List(ctx, dprojections.DebtFilter{OwnerID: userID, Status: &status})
	if err == nil && len(debts) > 0 {
		var lines []string
		for _, d := range debts {
			dir := "owed to user"
			if d.Direction == "OUT" {
				dir = "user owes"
			}
			lines = append(lines, fmt.Sprintf("- \"%s\" (%d cents %s, %s)", d.Reason, d.Amount, d.Currency, dir))
		}
		parts = append(parts, "The user's unsettled debts:\n"+strings.Join(lines, "\n"))
	}

	// Recurring movements
	active := true
	rms, err := h.resolver.recurringStore.List(ctx, rmprojections.RecurringMovementFilter{Active: &active})
	if err == nil {
		var lines []string
		for _, rm := range rms {
			if rm.UserID == userID {
				lines = append(lines, fmt.Sprintf("- \"%s\" (%s %d cents %s on wallet)", rm.Reason, rm.Frequency, rm.Amount, rm.Type))
			}
		}
		if len(lines) > 0 {
			parts = append(parts, "The user's active recurring movements:\n"+strings.Join(lines, "\n"))
		}
	}

	if len(parts) == 0 {
		return "The user has no wallets, debts, or recurring movements yet."
	}

	return strings.Join(parts, "\n\n")
}
