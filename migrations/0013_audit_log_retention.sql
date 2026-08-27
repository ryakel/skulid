-- +goose Up
-- +goose StatementBegin

-- audit_log is the highest-churn table in the schema: smart-block and
-- decompression recompute both re-diff on every sync, so a busy calendar
-- appends rows continuously and forever. Nothing has ever pruned it, and the
-- UI only surfaces the last 200 -- everything older is pure disk.
--
-- The prune deletes by age, which without this index is a sequential scan of
-- the largest table there is.
CREATE INDEX IF NOT EXISTS audit_log_ts_idx ON audit_log(ts);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS audit_log_ts_idx;
-- +goose StatementEnd
