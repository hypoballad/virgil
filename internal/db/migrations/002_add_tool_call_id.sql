ALTER TABLE tool_calls ADD COLUMN tool_call_id TEXT;
CREATE INDEX idx_tool_calls_uuid ON tool_calls(tool_call_id);
