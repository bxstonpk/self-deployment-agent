-- Application logs are kept for 90 days (DEC-027, decided 2026-10-23) and
-- purged by platform-api's retention sweeper. The purge selects by age
-- alone, which neither existing index (both lead with another column)
-- serves.
CREATE INDEX application_logs_by_age ON application_logs (logged_at);
