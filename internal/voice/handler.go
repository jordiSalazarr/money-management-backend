package voice

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	dprojections "github.com/jordisalazar/money-management/internal/payments/debt/projections"
	rmprojections "github.com/jordisalazar/money-management/internal/payments/recurringmovement/projections"
	"github.com/jordisalazar/money-management/internal/shared/middleware"
)

// maxAudioBytes caps the multipart upload to /voice/audio. 60s of
// 16 kHz mono 16-bit PCM packaged as WAV is ~1.9 MB; 4 MB leaves
// headroom and stays well under API Gateway's 10 MB request limit.
const maxAudioBytes = 4 << 20

// Handler handles voice command HTTP requests.
type Handler struct {
	client      Backend
	transcriber Transcriber
	executor    *Executor
	resolver    *Resolver
}

func NewHandler(client Backend, transcriber Transcriber, executor *Executor, resolver *Resolver) *Handler {
	return &Handler{client: client, transcriber: transcriber, executor: executor, resolver: resolver}
}

type voiceRequest struct {
	Text string `json:"text"`
}

type voiceResponse struct {
	Status     string   `json:"status"`
	Message    string   `json:"message"`
	Options    []string `json:"options,omitempty"`
	Transcript string   `json:"transcript,omitempty"`
}

// HandleVoiceCommand processes a typed voice command.
// @Summary Process a typed voice command
// @Description Interprets natural-language text and executes the corresponding action. Used by the typed-input path; the speech path posts audio to /voice/audio instead.
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

	h.runCommand(c, userID, req.Text)
}

// HandleVoiceAudio transcribes an uploaded audio clip then runs the
// same interpret+execute pipeline as the typed endpoint.
// @Summary Process a spoken voice command
// @Description Accepts a WAV (PCM 16-bit mono) clip, transcribes it via AWS Transcribe, then interprets and executes the command.
// @Tags voice
// @Accept multipart/form-data
// @Produce json
// @Param audio formData file true "WAV audio (PCM 16-bit mono)"
// @Success 200 {object} voiceResponse
// @Failure 400 {object} voiceResponse
// @Failure 401 {object} voiceResponse
// @Failure 500 {object} voiceResponse
// @Security BearerAuth
// @Router /voice/audio [post]
func (h *Handler) HandleVoiceAudio(c *gin.Context) {
	userID := middleware.GetUserID(c.Request.Context())
	if userID == "" {
		c.JSON(http.StatusUnauthorized, voiceResponse{
			Status:  "error",
			Message: "Unauthorized",
		})
		return
	}

	// Cap the body before reading anything so a misbehaving client
	// can't blow up Lambda memory.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxAudioBytes)

	file, _, err := c.Request.FormFile("audio")
	if err != nil {
		c.JSON(http.StatusBadRequest, voiceResponse{
			Status:  "error",
			Message: "Missing 'audio' file in form data",
		})
		return
	}
	defer file.Close()

	audio, err := io.ReadAll(file)
	if err != nil {
		c.JSON(http.StatusBadRequest, voiceResponse{
			Status:  "error",
			Message: "Audio upload too large or unreadable",
		})
		return
	}

	slog.Info("voice audio received", "user_id", userID, "bytes", len(audio))

	text, err := h.transcriber.Transcribe(c.Request.Context(), audio)
	if err != nil {
		slog.Error("transcription failed", "error", err, "user_id", userID)
		c.JSON(http.StatusInternalServerError, voiceResponse{
			Status:  "error",
			Message: "Failed to transcribe audio. Please try again.",
		})
		return
	}
	if text == "" {
		c.JSON(http.StatusOK, voiceResponse{
			Status:  "error",
			Message: "Could not hear anything. Try speaking closer to the mic.",
		})
		return
	}

	slog.Info("voice audio transcribed", "user_id", userID, "text", text)
	h.runCommand(c, userID, text)
}

// runCommand is the shared tail of both endpoints: turn natural-
// language text into a tool call, execute it, write the response.
// transcript is echoed back so the UI can show what the user actually
// said — useful when Transcribe mishears.
func (h *Handler) runCommand(c *gin.Context, userID, text string) {
	slog.Info("voice command received", "user_id", userID, "text", text)

	userContext := h.buildUserContext(c.Request.Context(), userID)

	tools := AllTools()
	result, err := h.client.Interpret(c.Request.Context(), text, tools, userContext)
	if err != nil {
		slog.Error("claude interpretation failed", "error", err)
		c.JSON(http.StatusInternalServerError, voiceResponse{
			Status:     "error",
			Message:    "Failed to interpret command. Please try again.",
			Transcript: text,
		})
		return
	}

	if result.Text != "" {
		c.JSON(http.StatusOK, voiceResponse{
			Status:     "error",
			Message:    result.Text,
			Transcript: text,
		})
		return
	}

	slog.Info("voice command interpreted", "user_id", userID, "tool", result.ToolName)

	execResult := h.executor.Execute(c.Request.Context(), userID, result.ToolName, result.ToolInput)

	if execResult.NeedsClarify {
		c.JSON(http.StatusOK, voiceResponse{
			Status:     "clarification_needed",
			Message:    execResult.ClarifyPrompt,
			Options:    execResult.Options,
			Transcript: text,
		})
		return
	}

	if !execResult.Success {
		c.JSON(http.StatusOK, voiceResponse{
			Status:     "error",
			Message:    execResult.Message,
			Transcript: text,
		})
		return
	}

	c.JSON(http.StatusOK, voiceResponse{
		Status:     "success",
		Message:    execResult.Message,
		Transcript: text,
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
