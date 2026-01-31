package engine

type EventType string

// Event type constants
const (
	EventEsc               EventType = "esc"
	EventTextChanged       EventType = "text_changed"
	EventTextChangeTimeout EventType = "trigger_completion"
	EventCursorMovedNormal EventType = "cursor_moved_normal"
	EventInsertEnter       EventType = "insert_enter"
	EventInsertLeave       EventType = "insert_leave"
	EventAccept            EventType = "accept"
	EventPartialAccept     EventType = "partial_accept"
	EventIdleTimeout       EventType = "idle_timeout"
	EventCompletionReady   EventType = "completion_ready"
	EventCompletionError   EventType = "completion_error"
	EventPrefetchReady     EventType = "prefetch_ready"
	EventPrefetchError     EventType = "prefetch_error"

	// Streaming events (handled directly via channel selection, not through eventChan)
	EventStreamLine     EventType = "stream_line"     // A line was received from the stream
	EventStreamComplete EventType = "stream_complete" // Stream completed
	EventStreamError    EventType = "stream_error"    // Stream error
)

var eventTypeMap map[string]EventType

func init() {
	eventTypeMap = buildEventTypeMap()
}

func buildEventTypeMap() map[string]EventType {
	eventMap := make(map[string]EventType)

	// Create a slice of all known EventType values
	allEventTypes := []EventType{
		EventEsc,
		EventTextChanged,
		EventTextChangeTimeout,
		EventCursorMovedNormal,
		EventInsertEnter,
		EventInsertLeave,
		EventAccept,
		EventPartialAccept,
		EventIdleTimeout,
		EventCompletionReady,
		EventCompletionError,
		EventPrefetchReady,
		EventPrefetchError,
		EventStreamLine,
		EventStreamComplete,
		EventStreamError,
	}

	// Build the map from EventType value to string
	for _, eventType := range allEventTypes {
		eventMap[string(eventType)] = eventType
	}

	return eventMap
}

func EventTypeFromString(s string) EventType {
	if eventType, exists := eventTypeMap[s]; exists {
		return eventType
	}
	return "" // or return a default EventType
}

type Event struct {
	Type EventType
	Data any
}

// Handler methods have been moved to completion/ package
// See: engine/completion/handler.go
