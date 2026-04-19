package parser

import "testing"

func TestParseLineReturnsNilForBlankAndInvalidInput(t *testing.T) {
	if ParseLine("   \n") != nil {
		t.Fatal("expected nil for blank line")
	}

	if ParseLine("not-json") != nil {
		t.Fatal("expected nil for invalid json")
	}
}

func TestParseLinePreservesSessionFieldsAndRawPayload(t *testing.T) {
	line := `{"type":"stream_event","session_id":"sid-1","event":{"type":"message_start","message":{"id":"msg-1"}},"extra":"value"}`
	parsed := ParseLine(line)
	if parsed == nil {
		t.Fatal("expected parsed event")
	}

	if parsed.Type != "stream_event" {
		t.Fatalf("unexpected type: %q", parsed.Type)
	}
	if parsed.SessionID != "sid-1" {
		t.Fatalf("unexpected session id: %q", parsed.SessionID)
	}
	if parsed.Event == nil {
		t.Fatal("expected stream event payload")
	}
	if parsed.Raw["extra"] != "value" {
		t.Fatalf("expected raw extra field, got %#v", parsed.Raw["extra"])
	}
}

func TestParseLinePreservesAssistantMessageIDAndAPIRetryFields(t *testing.T) {
	assistant := ParseLine(`{"type":"assistant","message":{"id":"msg-42","content":[{"type":"text","text":"hello"}]}}`)
	if assistant == nil || assistant.Message == nil {
		t.Fatal("expected assistant message")
	}
	if assistant.Message.ID != "msg-42" {
		t.Fatalf("unexpected message id: %q", assistant.Message.ID)
	}

	retry := ParseLine(`{"type":"system","subtype":"api_retry","attempt":1,"max_retries":10,"retry_delay_ms":600,"error_status":401,"error":"authentication_failed"}`)
	if retry == nil {
		t.Fatal("expected api retry event")
	}
	if retry.Attempt == nil || *retry.Attempt != 1 {
		t.Fatalf("unexpected attempt: %#v", retry.Attempt)
	}
	if retry.MaxRetries == nil || *retry.MaxRetries != 10 {
		t.Fatalf("unexpected max retries: %#v", retry.MaxRetries)
	}
	if retry.RetryDelayMS == nil || *retry.RetryDelayMS != 600 {
		t.Fatalf("unexpected retry delay: %#v", retry.RetryDelayMS)
	}
	if retry.ErrorStatus == nil || *retry.ErrorStatus != 401 {
		t.Fatalf("unexpected error status: %#v", retry.ErrorStatus)
	}
	if retry.Error != "authentication_failed" {
		t.Fatalf("unexpected error: %q", retry.Error)
	}
}
