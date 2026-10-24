-- A deleted application frees its name (the platform owner, 2026-10-24):
-- an application that failed without ever running is removed, and its
-- owner deploys again under the same name. The name it had is kept here.
ALTER TABLE applications ADD COLUMN former_name TEXT;
