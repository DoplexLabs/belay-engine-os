ALTER TABLE transcript_project_analysis_state
ADD COLUMN analysis_version TEXT NOT NULL DEFAULT '';

UPDATE transcript_project_analysis_state
SET analyzed_generation = 0,
    analyzed_at = NULL,
    analysis_version = '';
