CREATE INDEX CONCURRENTLY idx_chat_message_agent_author ON chat_message (author_id, created_at) WHERE author_type = 'agent';
