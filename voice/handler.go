package voice

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/shared/middleware"
)

// Handler handles voice command HTTP requests.
type Handler struct {
	client   *Client
	executor *Executor
}

func NewHandler(client *Client, executor *Executor) *Handler {
	return &Handler{client: client, executor: executor}
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

	// Step 1: Send to Claude for interpretation.
	tools := AllTools()
	result, err := h.client.Interpret(c.Request.Context(), req.Text, tools)
	if err != nil {
		slog.Error("claude interpretation failed", "error", err)
		c.JSON(http.StatusInternalServerError, voiceResponse{
			Status:  "error",
			Message: "Failed to interpret command. Please try again.",
		})
		return
	}

	// Step 2: If Claude returned text instead of a tool call, it couldn't understand.
	if result.Text != "" {
		c.JSON(http.StatusOK, voiceResponse{
			Status:  "error",
			Message: result.Text,
		})
		return
	}

	slog.Info("voice command interpreted", "user_id", userID, "tool", result.ToolName)

	// Step 3: Execute the command.
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
