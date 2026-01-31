package engine

// doPartialAcceptCompletion is the action for EventPartialAccept from stateHasCompletion
func (e *Engine) doPartialAcceptCompletion(event Event) {
	e.partialAcceptCompletion()
}

// doPartialAcceptStreaming is the action for EventPartialAccept from stateStreamingCompletion
func (e *Engine) doPartialAcceptStreaming(event Event) {
	// Only allow if first stage was rendered during streaming
	if e.streamingState != nil && e.streamingState.FirstStageRendered {
		e.cancelLineStreamingKeepPartial()
		e.partialAcceptCompletion()
	}
}
