-- 200 campaigns (ids 1-200). Every 10th id is PAUSED, giving exactly 20 paused
-- campaigns that stay the same after every `docker compose down -v`.
INSERT INTO campaigns (id, name, status)
WITH RECURSIVE seq (n) AS (
  SELECT 1
  UNION ALL
  SELECT n + 1 FROM seq WHERE n < 200
)
SELECT n,
       CONCAT('Campaign ', n),
       IF(n % 10 = 0, 'PAUSED', 'ACTIVE')
FROM seq;
