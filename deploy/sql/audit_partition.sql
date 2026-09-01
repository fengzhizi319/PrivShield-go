-- =============================================================================
-- PrivShield 数盾 —— audit_logs 按月 RANGE 分区迁移与整段清理运维脚本（P2-12 配套）
-- Monthly RANGE partitioning for the evidence table + whole-partition purge.
-- =============================================================================
-- 这份脚本解决什么
--   存证表按自然月分区后，「按时间扫全表」变成「只碰相关月分区」，且到期数据的整段
--   下线可以用 DROP PARTITION（秒级、不产生死元组、不膨胀 WAL）代替 DELETE。
--
-- 适用对象与时机（重要，别在跑着的系统上随手执行）
--   • 面向**新装/下一套安装**与计划内迁移窗口：脚本会把现有 audit_logs 全量拷贝到
--     新的分区表再换名，属于「停写 + 全表复制」操作，期间存证服务必须已停止
--     （否则拷贝窗口内的新证据既不在旧表也不在新表 → 直接丢证）。
--   • 应用侧本身**不依赖分区**：services/audit-log 的留存清理走的是
--     store.CleanupOld / `DELETE FROM audit_logs WHERE timestamp < $1`
--     与 archive.ArchiveAndCleanup 的按页 DELETE（pkg/store/postgres/audit.go）。
--     所以分区化是**运维层面的可选项**，不是合规必需路径。
--   • 已在分区表上运行、或数据量很小的实例不要重复执行：步骤 1 之前有守卫，
--     重复执行会在预检查处报错退出，不会破坏现有表。
--
-- ⚠️ 必须知道的语义代价（不是疏漏，是取舍）
--   1) 主键从 `(id)` 变成 `(id, timestamp)`：分区表的每个唯一约束都必须包含分区键。
--      于是 `id` 的全局唯一性不再由数据库强制——Go 侧写入的是 UUID v4（业务层保证唯一），
--      且所有查询/更新/删除都带 id + timestamp 语义；但请知悉：**数据库不再替你挡住重复 id**。
--   2) `snapshots.audit_log_id REFERENCES audit_logs(id) ON DELETE CASCADE` 必须放弃：
--      被引用侧的键现在是 (id, timestamp)，只引用 id 建不了外键。
--      留存清理路径不受影响（它本来就**先删 snapshots 再删 audit_logs**，
--      pkg/store/postgres/audit.go 的按页删除即如此），但两条依赖 CASCADE 的旧语义没了：
--        - 手工执行 `DELETE FROM audit_logs WHERE ...`（即 CleanupOld）不再自动带走快照；
--        - 分区整段 DROP 后，该时段的 snapshots 变成孤儿，必须在同一窗口内单独清理（见步骤 7）。
--   3) **DROP PARTITION 完全绕过应用的「先归档后删除」守卫。**
--      应用侧的守卫（archive.Archiver.ArchiveAndCleanup）只做在 DELETE 路径上：
--      逐页读出 → 写 SM4-GCM 加密段 + SM3 行链 manifest → 回读验真 → 才删该页；
--      任一步失败即停止删除。而 DROP PARTITION 是属主级 DDL，不经任何应用代码，
--      删掉的证据**不会留下任何归档段**。因此本脚本不提供「一键 DROP」，
--      只提供带人工核对门槛的清理流程；未走完该流程就不要执行。
--
-- 证据必须先归档的实操程序（DROP PARTITION 之前的强制前置）
--   1. 停止（或确认 AUDIT_LOG_RETENTION_DAYS=0 且无其他清理任务在跑），锁定待清理月份 M。
--   2. 用应用侧归档产出该月证据段：把 AUDIT_LOG_RETENTION_DAYS 临时设为「恰好让 cutoff
--      落在 M 月末」的值（必须 ≥1095 且 AUDIT_LOG_ENCRYPTION_KEY、AUDIT_LOG_ARCHIVE_DIR
--      均已配置，否则服务拒绝启动），让 6 小时一轮的留存循环对 M 月执行
--      「先归档后删除」，产出：
--         audit-archive-<cutoff>-<seq>.ndjson.gz.enc   段格式 privshield-audit-archive/v1
--         audit-archive-<cutoff>-<seq>.manifest.json   行链 SM3-LINE-CHAIN:v1
--                                                      加密 SM4-GCM/HKDF-SM3(enc:v2)
--      段与 manifest 的 SHA-256/大小/记录数登记进变更单，并做异介质副本。
--   3. 离线核对：archive.VerifySegment(dir, segment, key) 逐段验链。
--      ⚠️ 诚实说明：该函数目前**只有 Go API，仓库内没有命令行封装**，核对必须由开发同学
--         提供一次性调用（或后续补 cmd 入口）；不要跳过这一步就 DROP。
--   4. 只有当 M 月每一段都验真通过、且离线副本已就位，才执行步骤 7 的整段下线。
--   5. 记录：DROP 的分区名、行数、对应归档段清单与验真结论，形成可举证的销毁记录。
--
-- 前置条件
--   • PostgreSQL 16（compose 固定 postgres:16-alpine）；不使用 pg_partman / pg_cron：
--     deploy/postgres/postgresql.conf 里 shared_preload_libraries = ''，
--     分区生命周期必须由可审计的计划任务显式创建，理由见该文件第 8 节。
--   • 以表属主（即服务连接账号 / 迁移角色）执行，不能用 privshield_audit_writer
--     （只写角色没有 DDL 权限，跑不动本脚本——这正是它该有的行为）。
--   • 已做一份可回滚备份（pg_dump 或基础备份）。本脚本自身的回滚只是「换名换回去」，
--     不覆盖拷贝期间的应用写失败。
--
-- 执行方式
--   docker compose -f docker-compose.prod.yml stop audit-log          # 先停写
--   docker compose -f docker-compose.prod.yml exec phase-b-postgres \
--     psql -U "$PG_USER" -d "$PG_DATABASE" -c "SET privshield.partition_migration_ok = 'yes';"  # 见步骤 0
--   psql "postgresql://<属主>@<host>:5432/<db>?sslmode=require" -1 -f deploy/sql/audit_partition.sql
--   （-1 单事务：拷贝与换名要么整体成功，要么整体回滚，不留半套表。）
--   注意：步骤 0 的确认 GUC 是会话级的，与 -1 同时使用时请把 SET 放进同一个 -c 里，
--   或改用 `-c "SET privshield.partition_migration_ok='yes'" -f ...` 由 psql 合并会话。
-- =============================================================================

\set ON_ERROR_STOP on

-- -----------------------------------------------------------------------------
-- 步骤 0：防误跑门槛 + 环境预检（任一不满足即抛错，不做任何变更）
-- -----------------------------------------------------------------------------
DO $$
DECLARE
    rel_kind   "char";
    part_kind  "char";
BEGIN
    -- 0.a 必须显式确认：本脚本会造成全表拷贝与停写窗口。
    IF coalesce(current_setting('privshield.partition_migration_ok', true), '') <> 'yes' THEN
        RAISE EXCEPTION '需要显式确认：请先 SET privshield.partition_migration_ok = ''yes''（见脚本头部「执行方式」）';
    END IF;

    -- 0.b 目标表必须存在且是**普通表**（已经是分区表就说明迁移做过了）。
    SELECT c.relkind INTO rel_kind
      FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
     WHERE n.nspname = 'public' AND c.relname = 'audit_logs';
    IF rel_kind IS NULL THEN
        RAISE EXCEPTION 'public.audit_logs 不存在：请先由迁移角色执行 deploy/sql/audit_writeonly_role.sql 的步骤 0 建表';
    END IF;
    IF rel_kind = 'p' THEN
        RAISE EXCEPTION 'public.audit_logs 已经是分区表，本脚本只用于一次性迁移，拒绝重复执行';
    END IF;
    IF rel_kind <> 'r' THEN
        RAISE EXCEPTION 'public.audit_logs 不是普通表（relkind=%），终止', rel_kind;
    END IF;

    -- 0.c 不允许残留上一次中断的中间对象（避免把半成品当成果表）。
    SELECT c.relkind INTO part_kind
      FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
     WHERE n.nspname = 'public' AND c.relname = 'audit_logs_partitioned';
    IF part_kind IS NOT NULL THEN
        RAISE EXCEPTION 'public.audit_logs_partitioned 已存在（疑似上次迁移残留），请人工确认后清理再重跑';
    END IF;
    IF to_regclass('public.audit_logs_legacy_unpartitioned') IS NOT NULL THEN
        RAISE EXCEPTION 'public.audit_logs_legacy_unpartitioned 已存在（疑似上次迁移残留），请人工确认后清理再重跑';
    END IF;

    -- 0.d 停写检查：仍有多个活跃连接在用该库时，说明存证服务没停，拷贝窗口会丢证。
    IF (SELECT count(*) FROM pg_stat_activity
         WHERE datname = current_database()
           AND pid <> pg_backend_pid()
           AND state <> 'idle') > 0 THEN
        RAISE EXCEPTION '检测到其他活跃连接：请先停止 audit-log 服务再迁移（拷贝期间写入的新存证会丢失）';
    END IF;
END
$$;

-- -----------------------------------------------------------------------------
-- 步骤 1：建立按月分区的父表
-- 列定义与 pkg/store/postgres/audit.go 的 initAuditSchema 逐列一致；
-- 唯一差异是主键必须包含分区键 timestamp（见头部「语义代价」第 1 条）。
-- -----------------------------------------------------------------------------
CREATE TABLE audit_logs_partitioned (
    id              TEXT NOT NULL,
    task_id         TEXT DEFAULT '',
    api_code        TEXT DEFAULT '',
    datasource_id   TEXT DEFAULT '',
    timestamp       TIMESTAMPTZ NOT NULL,
    operation       TEXT,
    datasource      TEXT,
    input_hash      TEXT,
    output_hash     TEXT,
    algorithm       TEXT,
    parameters_json TEXT,
    input_rows      INTEGER DEFAULT 0,
    output_rows     INTEGER DEFAULT 0,
    duration_ms     BIGINT DEFAULT 0,
    user_name       TEXT,
    status          TEXT,
    error_message   TEXT,
    security_level  TEXT,
    prev_hash       TEXT DEFAULT '',
    integrity_hash  TEXT DEFAULT '',
    CONSTRAINT audit_logs_partitioned_pkey PRIMARY KEY (id, timestamp)
) PARTITION BY RANGE (timestamp);
-- 刻意**不建 DEFAULT 分区**：DEFAULT 会把落在管理区间之外的证据（时钟漂移、
-- 回填历史、预创建月份用完）悄悄吞掉，而这些分区永远不会出现在「按月清理」清单里，
-- 等于制造无人看管的证据暗格。超期写入应当直接失败并被看到。

-- -----------------------------------------------------------------------------
-- 步骤 2：按月创建分区（覆盖已有数据所在月份 + 未来 N 个月）
-- 未来月份预创建数量：改 n_future_months。默认 12 个月，配合下面的「月度追加」小节。
-- -----------------------------------------------------------------------------
DO $$
DECLARE
    n_future_months constant int := 12;
    first_ts        timestamptz;
    last_ts         timestamptz;
    m_start         timestamptz;
    m_end           timestamptz;
    horizon_end     timestamptz;
    created         int := 0;
BEGIN
    SELECT min(timestamp), max(timestamp) INTO first_ts, last_ts FROM audit_logs;
    IF first_ts IS NULL THEN
        first_ts := date_trunc('month', now());
        last_ts  := first_ts;
    END IF;

    m_start     := date_trunc('month', first_ts);
    horizon_end := date_trunc('month', now()) + make_interval(months => n_future_months + 1);
    IF last_ts >= horizon_end THEN
        horizon_end := date_trunc('month', last_ts) + interval '1 month';
    END IF;

    WHILE m_start < horizon_end LOOP
        m_end := m_start + interval '1 month';
        EXECUTE format(
            'CREATE TABLE %I PARTITION OF audit_logs_partitioned FOR VALUES FROM (%L) TO (%L)',
            'audit_logs_' || to_char(m_start, 'YYYYMM'), m_start, m_end);
        created := created + 1;
        m_start := m_end;
    END LOOP;

    -- 拷贝前的边界自检：任何落在分区范围之外的行都必须是问题，宁可报错也不静默丢弃。
    IF EXISTS (SELECT 1 FROM audit_logs WHERE timestamp >= horizon_end) THEN
        RAISE EXCEPTION '存在 timestamp >= % 的存证（预创建月份不足）：请增大 n_future_months 后重跑', horizon_end;
    END IF;

    RAISE NOTICE '分区创建完成：共 % 个月分区，覆盖至 %', created, horizon_end;
END
$$;

-- -----------------------------------------------------------------------------
-- 步骤 3：按月拷贝 + 逐月数量核对（单事务内，任一月份对不上即整体回滚）
-- -----------------------------------------------------------------------------
DO $$
DECLARE
    m_start timestamptz;
    m_end   timestamptz;
    last_ts timestamptz;
    src     bigint;
    dst     bigint;
    total_src bigint := 0;
    total_dst bigint := 0;
BEGIN
    SELECT max(timestamp) INTO last_ts FROM audit_logs;
    IF last_ts IS NULL THEN
        RAISE NOTICE 'audit_logs 为空表：仅完成分区表建表，无需拷贝';
        RETURN;
    END IF;

    m_start := date_trunc('month', (SELECT min(timestamp) FROM audit_logs));
    WHILE m_start <= last_ts LOOP
        m_end := m_start + interval '1 month';

        EXECUTE $fmt$
            INSERT INTO audit_logs_partitioned (
                id, task_id, api_code, datasource_id, timestamp, operation, datasource,
                input_hash, output_hash, algorithm, parameters_json, input_rows, output_rows,
                duration_ms, user_name, status, error_message, security_level, prev_hash, integrity_hash)
            SELECT id, task_id, api_code, datasource_id, timestamp, operation, datasource,
                input_hash, output_hash, algorithm, parameters_json, input_rows, output_rows,
                duration_ms, user_name, status, error_message, security_level, prev_hash, integrity_hash
              FROM audit_logs WHERE timestamp >= $1 AND timestamp < $2
        $fmt$ USING m_start, m_end;

        EXECUTE 'SELECT count(*) FROM audit_logs WHERE timestamp >= $1 AND timestamp < $2'
            INTO src USING m_start, m_end;
        EXECUTE 'SELECT count(*) FROM audit_logs_partitioned WHERE timestamp >= $1 AND timestamp < $2'
            INTO dst USING m_start, m_end;

        IF src <> dst THEN
            RAISE EXCEPTION '拷贝校验失败：分区 % 源 % 行 / 目标 % 行',
                to_char(m_start, 'YYYYMM'), src, dst;
        END IF;

        total_src := total_src + src;
        total_dst := total_dst + dst;
        m_start := m_end;
    END LOOP;

    IF total_src <> total_dst OR total_src <> (SELECT count(*) FROM audit_logs) THEN
        RAISE EXCEPTION '总量校验失败：源 % / 目标 %', total_src, total_dst;
    END IF;
    RAISE NOTICE '拷贝校验通过：% 行逐月一致', total_dst;
END
$$;

-- -----------------------------------------------------------------------------
-- 步骤 4：拷贝后先 ANALYZE（二级索引刻意留到步骤 5 换名之后再建，理由见该步）
-- -----------------------------------------------------------------------------
ANALYZE audit_logs_partitioned;

-- -----------------------------------------------------------------------------
-- 步骤 5：断开快照外键 → 换名（含让出索引名）→ 在新分区表上建规范名索引
--
-- 为什么索引必须放在换名之后建：pkg/store/postgres/audit.go 每次启动都会执行
--   CREATE INDEX IF NOT EXISTS idx_audit_logs_timestamp ON audit_logs (timestamp DESC); ...
-- 而**索引名在同库内全局唯一**。若新表沿用 *_v2 之类的别名，旧表仍占着这四个名字，
-- Go 的 IF NOT EXISTS 会因为「重名」而静默跳过，分区表最终**没有**时间索引，
-- 存证查询退化为跨全部分区顺序扫描且无人报错。这里先把旧表索引改名让位，再用规范名建。
-- -----------------------------------------------------------------------------
DO $$
DECLARE
    con name;
BEGIN
    SELECT c.conname INTO con
      FROM pg_constraint c
      JOIN pg_class t ON t.oid = c.conrelid
      JOIN pg_namespace n ON n.oid = t.relnamespace
     WHERE n.nspname = 'public' AND t.relname = 'snapshots' AND c.contype = 'f';
    IF con IS NOT NULL THEN
        EXECUTE format('ALTER TABLE public.snapshots DROP CONSTRAINT %I', con);   -- CASCADE 到此为止
        RAISE NOTICE '已移除外键 %（分区表无法只被 id 引用）：留存清理不受影响，'
                     '但 CleanupOld/DROP PARTITION 之后必须显式清理 snapshots', con;
    END IF;
END
$$;

ALTER TABLE audit_logs             RENAME TO audit_logs_legacy_unpartitioned;

-- 旧表索引统一加 legacy_ 前缀，让出 idx_audit_logs_* 这四个规范名。
DO $$
DECLARE
    r record;
BEGIN
    FOR r IN
        SELECT x.indexrelid::regclass::text AS idx_ref, i.relname AS old_name
          FROM pg_index x
          JOIN pg_class i ON i.oid = x.indexrelid
          JOIN pg_class c ON c.oid = x.indrelid
          JOIN pg_namespace n ON n.oid = c.relnamespace
         WHERE n.nspname = 'public' AND c.relname = 'audit_logs_legacy_unpartitioned'
    LOOP
        EXECUTE format('ALTER INDEX %s RENAME TO %I', r.idx_ref, 'legacy_' || r.old_name);
        RAISE NOTICE '旧表索引改名为 legacy_%', r.old_name;
    END LOOP;
END
$$;

ALTER TABLE audit_logs_partitioned RENAME TO audit_logs;

-- 规范名索引（建在分区父表上 → 自动下沉到现有分区并为其后新建分区自动建）。
CREATE INDEX IF NOT EXISTS idx_audit_logs_timestamp     ON audit_logs (timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_audit_logs_operation     ON audit_logs (operation);
CREATE INDEX IF NOT EXISTS idx_audit_logs_datasource_id ON audit_logs (datasource_id);
CREATE INDEX IF NOT EXISTS idx_audit_logs_task_id       ON audit_logs (task_id);

-- 保留旧表作为回滚窗口（默认至少一个留存/备份周期）；确认新表健康后再人工 DROP。
-- 回滚办法（仅需换名，数据未从旧表删除过）：
--   ALTER TABLE audit_logs RENAME TO audit_logs_partitioned;      -- 索引名会冲突，先把上面四条 DROP
--   ALTER TABLE audit_logs_legacy_unpartitioned RENAME TO audit_logs;
--   （然后重新加回 snapshots 的外键，并把 legacy_ 前缀索引改回原名。）

-- -----------------------------------------------------------------------------
-- 步骤 6：换名后校验 + 权限重授
-- -----------------------------------------------------------------------------
DO $$
DECLARE
    new_rows bigint;
    old_rows bigint;
    idx_count  int;
BEGIN
    IF (SELECT partrelid FROM pg_partitioned_table
         JOIN pg_class ON pg_class.oid = pg_partitioned_table.partrelid
        WHERE relname = 'audit_logs') IS NULL THEN
        RAISE EXCEPTION 'public.audit_logs 未成为分区表，换名失败';
    END IF;
    SELECT count(*) INTO new_rows FROM audit_logs;
    SELECT count(*) INTO old_rows FROM audit_logs_legacy_unpartitioned;
    IF new_rows <> old_rows THEN
        RAISE EXCEPTION '换名后行数不一致：新表 % / 旧表 %', new_rows, old_rows;
    END IF;

    -- 索引归属校验：四个规范名索引必须已挂在新分区表上（否则应用启动不会再建它们）。
    SELECT count(*) INTO idx_count
      FROM pg_index x
      JOIN pg_class c ON c.oid = x.indrelid
      JOIN pg_class i ON i.oid = x.indexrelid
     WHERE c.relname = 'audit_logs'
       AND i.relname IN ('idx_audit_logs_timestamp','idx_audit_logs_operation',
                         'idx_audit_logs_datasource_id','idx_audit_logs_task_id');
    IF idx_count <> 4 THEN
        RAISE EXCEPTION '规范名索引缺失（应为 4，实为 %）：应用启动时不会再补建，必须排查', idx_count;
    END IF;

    RAISE NOTICE '换名校验通过：% 行、索引 4/4；旧表 audit_logs_legacy_unpartitioned 保留待人工处置', new_rows;
END
$$;

-- 权限不随重命名转移：换名后必须以迁移角色**重新执行** deploy/sql/audit_writeonly_role.sql。
-- 分区父表上的表级授权会自动覆盖其下所有分区（含将来新建的分区），无需逐分区 GRANT。

-- -----------------------------------------------------------------------------
-- 步骤 7：到期证据的整段下线（可选路径，替代 DELETE）
-- ⚠️ 只有完成脚本头部「证据必须先归档的实操程序」全部 5 步才允许执行本节。
--     DROP PARTITION 不经过应用代码，不会产出归档段，也不会被 ArchiveAndCleanup 的
--     fail-closed 守卫拦下——它是这条红线上唯一需要**纯人工**把关的通道。
-- -----------------------------------------------------------------------------
-- 7.a 先看清要下线的分区里到底有什么（行数、时间跨度、链尾）：
--   SELECT count(*) AS rows, min(timestamp) AS from_ts, max(timestamp) AS to_ts,
--          max(integrity_hash) AS sample_chain_tail
--     FROM audit_logs_202501;                        -- 目标月份
--
-- 7.b 归档核对完成后，脱离并删除（DETACH 单独一步是为了在真正删除前还能再读一次）：
--   ALTER TABLE audit_logs DETACH PARTITION audit_logs_202501;
--   -- 建议先把 detached 表再挂一个只读副本/备份，然后：
--   DROP TABLE audit_logs_202501;
--   （PG16 没有 `ALTER TABLE ... DROP PARTITION` 语法——那是 PG17 才有的单步写法，
--     在 16 上执行会直接 syntax error；升级到 PG17 后可用：
--      ALTER TABLE audit_logs DROP PARTITION audit_logs_202501;
--     但它一步到位、不留「脱离后复核」的中间点，本流程仍推荐 DETACH + DROP 两步。）
--
-- 7.c CASCADE 已不存在：同一时间窗内的快照必须一起清理，否则永远留在表里（并继续占空间、
--     继续可能被查询接口读到）：
--   DELETE FROM snapshots
--    WHERE timestamp >= '2025-01-01T00:00:00+08:00'
--      AND timestamp <  '2025-02-01T00:00:00+08:00';
--   （如需可回收空间：VACUUM (ANALYZE) snapshots;）
--
-- 7.d 想要「孤儿检测」兜底，可启用下面的可选触发器（有写入开销，默认不启用）：
--   -- CREATE OR REPLACE FUNCTION forbid_orphan_snapshots() RETURNS trigger AS $$
--   -- BEGIN
--   --   IF NOT EXISTS (SELECT 1 FROM audit_logs a WHERE a.id = NEW.audit_log_id) THEN
--   --     RAISE EXCEPTION 'snapshot.audit_log_id=% 在 audit_logs 中不存在（分区被整段下线？）', NEW.audit_log_id;
--   --   END IF;
--   --   RETURN NEW;
--   -- END $$ LANGUAGE plpgsql;
--   -- CREATE TRIGGER snapshots_no_orphan BEFORE INSERT OR UPDATE OF audit_log_id
--   --   ON snapshots FOR EACH ROW EXECUTE FUNCTION forbid_orphan_snapshots();

-- -----------------------------------------------------------------------------
-- 附录：月度维护（预创建下一批分区）——pg_partman 不装，就得有人按期跑这段
-- 建议由部署侧计划任务（systemd timer / cron / CI 定时流水线）每月执行一次，
-- 并把「已存在的分区月份清单」纳入巡检（与 postgresql.conf 第 8 节的约束一致：
-- 分区生命周期必须可人工审计，不能交给后台扩展自动改写）。
-- -----------------------------------------------------------------------------
DO $$
DECLARE
    n_future_months constant int := 12;   -- 每次把未来窗口补满到 12 个月
    m_start         timestamptz;
    m_end           timestamptz;
    total           int;
BEGIN
    IF to_regclass('public.audit_logs') IS NULL
       OR (SELECT relkind FROM pg_class WHERE oid = to_regclass('public.audit_logs')) <> 'p' THEN
        RAISE NOTICE 'public.audit_logs 不存在或不是分区表：跳过（月度维护只用于已完成步骤 1~6 的实例）';
        RETURN;
    END IF;

    -- 只需保证「本月 + 未来 N 个月」存在：历史月份已由步骤 2 的全量迁移覆盖；
    -- 若确实发现历史空洞（例如曾停用一段时间），把 m_start 改成
    -- date_trunc('month', (SELECT min(timestamp) FROM audit_logs)) 再从缺口起补。
    m_start := date_trunc('month', now());
    WHILE m_start < date_trunc('month', now()) + make_interval(months => n_future_months + 1) LOOP
        m_end := m_start + interval '1 month';
        EXECUTE format(
            'CREATE TABLE IF NOT EXISTS %I PARTITION OF audit_logs FOR VALUES FROM (%L) TO (%L)',
            'audit_logs_' || to_char(m_start, 'YYYYMM'), m_start, m_end);
        m_start := m_end;
    END LOOP;

    SELECT count(*) INTO total FROM pg_inherits WHERE inhparent = to_regclass('public.audit_logs');
    RAISE NOTICE '月度维护完成：audit_logs 当前共 % 个月度分区，未来 % 个月已就位',
        total, n_future_months;
END
$$;
