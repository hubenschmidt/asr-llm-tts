package prompts

const DefaultSystem = "You are a helpful call center agent. Keep responses concise and conversational."

// ForSession resolves the final system prompt for a call session.
func ForSession(systemPrompt string) string {
	if systemPrompt != "" {
		return systemPrompt
	}
	return DefaultSystem
}

// RAGContext wraps retrieved knowledge base context into a system message.
func RAGContext(context string) string {
	return "Relevant context from knowledge base:\n" + context
}
