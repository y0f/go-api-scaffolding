-- +goose Up
-- +goose StatementBegin
-- Matches the list order (newest first, id as the tie-breaker), so a page is
-- read from the index instead of sorting the whole table.
DROP INDEX widgets_created_at_idx;
CREATE INDEX widgets_created_at_idx ON widgets (created_at DESC, id DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX widgets_created_at_idx;
CREATE INDEX widgets_created_at_idx ON widgets (created_at DESC);
-- +goose StatementEnd
