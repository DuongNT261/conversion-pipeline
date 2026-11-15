CREATE TABLE campaigns (
  id      BIGINT PRIMARY KEY,
  name    VARCHAR(255) NOT NULL,
  status  ENUM('ACTIVE', 'PAUSED') NOT NULL
);

CREATE TABLE import_jobs (
  id           BIGINT AUTO_INCREMENT PRIMARY KEY,
  file_name    VARCHAR(512) NOT NULL,
  file_sha256  CHAR(64) NOT NULL,
  status       VARCHAR(20) NOT NULL,           -- IMPORTING | PROCESSING | DONE
  total_rows   INT NOT NULL DEFAULT 0,
  created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  finished_at  DATETIME NULL,
  UNIQUE KEY uq_file (file_sha256)              -- the same file content is only one job
);

CREATE TABLE import_rows (
  id           BIGINT AUTO_INCREMENT PRIMARY KEY,
  job_id       BIGINT NOT NULL,
  line_no      INT NOT NULL,
  order_id     VARCHAR(64) NULL,
  click_id     VARCHAR(128) NULL,
  campaign_id  BIGINT NULL,
  amount       BIGINT NULL,
  order_time   DATETIME NULL,                   -- stored in UTC
  raw_line     TEXT NOT NULL,
  status       VARCHAR(20) NOT NULL,           -- see docs/01 section 5.2
  attempts     INT NOT NULL DEFAULT 0,
  error        VARCHAR(255) NULL,
  updated_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  UNIQUE KEY uq_job_line (job_id, line_no),     -- re-running the importer adds no duplicate rows
  KEY idx_status (status, id),                  -- relay looks up PENDING rows
  KEY idx_job_status (job_id, status)           -- per-job report
);

CREATE TABLE conversions (
  order_id      VARCHAR(64) PRIMARY KEY,        -- last line of defence against double reporting
  row_id        BIGINT NOT NULL,
  click_id      VARCHAR(128) NOT NULL,
  campaign_id   BIGINT NOT NULL,
  amount        BIGINT NOT NULL,
  platform_ref  VARCHAR(64) NOT NULL,
  sent_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
