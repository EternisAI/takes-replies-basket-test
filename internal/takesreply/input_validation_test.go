package takesreply

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestContinueMarksMalformedUserInput(t *testing.T) {
	message := func(value string) *string { return &value }
	valid := TurnInput{
		ThreadID: uuid.NewString(), ExpectedRevision: 0, RequestID: uuid.New(),
		Message: message("Please help me think this through."),
	}
	tooLong := strings.Repeat("a", maxTurnMessageRunes+1)
	walletKey := "0x" + strings.Repeat("ab", 32)
	aboveDatabaseRevision := int64(maxDatabaseRevision)
	aboveDatabaseRevision++

	tests := []struct {
		name  string
		input TurnInput
	}{
		{name: "missing message or choice", input: TurnInput{ThreadID: valid.ThreadID, RequestID: valid.RequestID, Message: message(" ")}},
		{name: "overlong message", input: TurnInput{ThreadID: valid.ThreadID, RequestID: valid.RequestID, Message: message(tooLong)}},
		{name: "wallet credential", input: TurnInput{ThreadID: valid.ThreadID, RequestID: valid.RequestID, Message: message(walletKey)}},
		{name: "invalid thread ID", input: TurnInput{ThreadID: "not-a-thread", RequestID: valid.RequestID, Message: valid.Message}},
		{name: "missing request ID", input: TurnInput{ThreadID: valid.ThreadID, Message: valid.Message}},
		{name: "negative revision", input: TurnInput{ThreadID: valid.ThreadID, ExpectedRevision: -1, RequestID: valid.RequestID, Message: valid.Message}},
		{name: "oversized revision", input: TurnInput{ThreadID: valid.ThreadID, ExpectedRevision: int(aboveDatabaseRevision), RequestID: valid.RequestID, Message: valid.Message}},
	}

	service := &Service{}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := service.Continue(context.Background(), "user-1", test.input)
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("Continue error = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestCreateNilRequestIDIsUserInputButUnavailableServiceIsInternal(t *testing.T) {
	service := &Service{}
	if _, err := service.CreateConversation(context.Background(), "user", uuid.Nil); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("nil request ID must be invalid input: %v", err)
	}
	var unavailable *Service
	if _, err := unavailable.CreateConversation(context.Background(), "user", uuid.New()); err == nil || errors.Is(err, ErrInvalidInput) {
		t.Fatalf("missing server service must remain internal: %v", err)
	}
	if _, err := service.CreateConversation(context.Background(), "", uuid.New()); err == nil || errors.Is(err, ErrInvalidInput) {
		t.Fatalf("missing trusted authentication must not become user input: %v", err)
	}
}
