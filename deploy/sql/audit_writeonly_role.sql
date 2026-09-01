-- =============================================================================
-- PrivShield 数盾 —— 存证库「只写不可改删」角色授权脚本（P1-6）
-- Write-only (append-only) role for the evidence database.
-- =============================================================================
-- 用途
--   创建 privshield_audit_writer 角色：对 public.audit_logs / public.snapshots 只有
--   CONNECT + schema USAGE + INSERT + SELECT，显式撤销 UPDATE / DELETE / TRUNCATE
--   （对角色与对 PUBLIC 各撤一次）。这样即使 API Key、BFF 与数据库凭据全部泄露，
--   攻击者也无法改写或删除已入链的存证——链式 prev_hash/integrity_hash 只防「篡改后
--   悄悄替换」，本脚本防的是「拿得到高权限账号就直接改删」这条更现实的路径。
--
-- 与应用侧启动自检的对应关系（services/audit-log/cmd/server/main.go → verifyWriteOnlyPostgres）
--   当 AUDIT_LOG_DB_WRITE_ONLY=true 时，服务启动会对两张表逐一执行
--     SELECT has_table_privilege(current_user, '<table>', 'UPDATE');  -- 必须为 false
--     SELECT has_table_privilege(current_user, '<table>', 'DELETE');  -- 必须为 false
--   任意为 true 即拒绝启动（属主隐含全部权限，所以属主账号也过不了自检）。
--
-- ⚠️ 执行顺序与前置条件（务必先读）
--   1) 表必须**先由迁移角色（DBA 的高权限账号）建好**再跑本脚本：本脚本只做授权，
--      不建表；若表不存在，GRANT 会直接报错（fail-fast，而不是给出半套权限）。
--      下文「步骤 0」提供与 Go 侧 pkg/store/postgres/audit.go 的 initAuditSchema
--      （audit_logs / snapshots 列定义、默认值、索引）**逐列一致**的建表语句，
--      仅供迁移角色在「服务从未启动过」的新装环境预建表；两边列定义一旦漂移必须同步修改。
--   2) 必须以超级用户或被授予 GRANT OPTION 的属主账号连接执行。
--   3) 口令不要写进本文件（会进 git）：用 psql 的 \password 交互设置。
--
-- ⚠️ 已知阻塞项（上线前必须知道，否则会把生产打不开）
--   当前 Go 代码在**每次启动**时都会执行 schema 初始化，其中包含
--     ALTER TABLE audit_logs  ADD COLUMN IF NOT EXISTS ...
--     ALTER TABLE snapshots   ADD COLUMN IF NOT EXISTS ...
--   （pkg/store/postgres/audit.go 的 initAuditSchema，与 CREATE TABLE 在同一个 Exec 里）。
--   ALTER TABLE 要求执行者是表属主，以 privshield_audit_writer 连接必然返回 42501，
--   服务在 AUDIT_LOG_STRICT_STORAGE=true 下会直接退出。因此在 Go 侧把「schema 初始化」
--   与「只写运行账号」分离（例如只写角色跳过 DDL、或提供独立的 migrate 入口）之前，
--   编排里 AUDIT_LOG_DB_WRITE_ONLY 只能保持 false——docker-compose.prod.yml 已按此
--   设默认值并在注释里指回本文件。届时改回 true 即可启用本脚本给出的角色。
--
-- ⚠️ 与自动清理的互斥关系（设计取舍，不是 bug）
--   「先归档后删除」的留存清理需要 DELETE 权限（archive.ArchiveAndCleanup 会按页
--   先删 snapshots 再删 audit_logs）。因此以只写角色连接时，AUDIT_LOG_RETENTION_DAYS
--   必须保持 0（永不物理删除）；到期证据的清理改由**分区整段剥离**承担，
--   流程见 deploy/sql/audit_partition.sql。两者不要同时打开。
--
-- 适用版本：PostgreSQL 16（deploy/docker-compose 固定 postgres:16-alpine）。
--   镜像升级到 PG17+ 后，可在步骤 4 追加撤销 MAINTAIN（PG17 新增的表级 VACUUM/ANALYZE 权限）；
--   PG16 及以下没有该权限名，写了会报 "unrecognized privilege type"。
--
-- 执行方式
--   psql "postgresql://<迁移角色>@<host>:5432/<db>" -1 --set ON_ERROR_STOP=on \
--        -f deploy/sql/audit_writeonly_role.sql
--   （-1 让整脚本在单事务内执行：要么全部授权成功，要么完全不生效。）
-- =============================================================================

\set ON_ERROR_STOP on

-- -----------------------------------------------------------------------------
-- 步骤 0（可选，且仅新装环境需要）：由迁移角色预建表结构
-- 与 pkg/store/postgres/audit.go 的 initAuditSchema 保持一致；已建好则整段跳过。
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS audit_logs (
    id              TEXT PRIMARY KEY,
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
    integrity_hash  TEXT DEFAULT ''
);

CREATE TABLE IF NOT EXISTS snapshots (
    id              TEXT PRIMARY KEY,
    audit_log_id    TEXT REFERENCES audit_logs(id) ON DELETE CASCADE,
    timestamp       TIMESTAMPTZ NOT NULL,
    input_sample    TEXT,
    output_sample   TEXT,
    algorithm       TEXT,
    parameters_json TEXT,
    integrity_hash  TEXT,
    prev_hash       TEXT DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_audit_logs_timestamp     ON audit_logs (timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_audit_logs_operation     ON audit_logs (operation);
CREATE INDEX IF NOT EXISTS idx_audit_logs_datasource_id ON audit_logs (datasource_id);
CREATE INDEX IF NOT EXISTS idx_audit_logs_task_id       ON audit_logs (task_id);
CREATE INDEX IF NOT EXISTS idx_snapshots_audit_log_id   ON snapshots (audit_log_id);

-- -----------------------------------------------------------------------------
-- 步骤 1：角色本体——最小权限，绝不给 SUPERUSER/CREATEROLE
-- 幂等写法：已存在则只刷新属性，不重建（重建会丢授权与已有连接）。
-- -----------------------------------------------------------------------------
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'privshield_audit_writer') THEN
        CREATE ROLE privshield_audit_writer WITH
            LOGIN
            NOSUPERUSER             -- 存证库账号不得具备绕过权限检查的能力
            NOCREATEDB
            NOCREATEROLE            -- 不得自我提权到能改授权
            NOINHERIT               -- 只靠直接授权，避免经由角色成员关系间接拿到 UPDATE/DELETE
            NOREPLICATION           -- 不需要逻辑复制/备库身份
            NOBYPASSRLS
            CONNECTION LIMIT 50;    -- 与 AUDIT_LOG_PG_MAX_CONNS 留出副本余量（见 postgresql.conf 第 7 节）
        RAISE NOTICE 'role privshield_audit_writer created';
    ELSE
        RAISE NOTICE 'role privshield_audit_writer already exists, attributes refreshed';
        ALTER ROLE privshield_audit_writer WITH
            LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS
            CONNECTION LIMIT 50;
    END IF;
END
$$;

-- 口令请在此处交互设置（本脚本故意不含 PASSWORD 子句，避免凭据入库）：
--   psql> \password privshield_audit_writer
--   非交互环境：PGPASSWORD 由部署系统的凭据管理注入，勿写进仓库。

-- -----------------------------------------------------------------------------
-- 步骤 2：库与 schema 级最小权限
-- -----------------------------------------------------------------------------
-- 只授当前连接库（\conninfo 可见）。CONNECT 是 pgx 建连的前提，但拿不到任何数据权限。
DO $$
DECLARE
    dbname text;
BEGIN
    SELECT current_database() INTO dbname;
    EXECUTE format('GRANT CONNECT ON DATABASE %I TO privshield_audit_writer', dbname);
    -- PUBLIC 默认持有 TEMPORARY ON DATABASE，这里单独对该角色收回：不允许在存证库里建临时表。
    EXECUTE format('REVOKE TEMPORARY ON DATABASE %I FROM privshield_audit_writer', dbname);
END
$$;

GRANT USAGE ON SCHEMA public TO privshield_audit_writer;

-- 反向收敛：PUBLIC 不得在 public schema 建对象（PG15+ 默认已撤销，此处对旧实例兜底）。
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
REVOKE CREATE ON SCHEMA public FROM privshield_audit_writer;

-- -----------------------------------------------------------------------------
-- 步骤 3：表级正向授权（INSERT 写证 / SELECT 验链与读取）
-- -----------------------------------------------------------------------------
GRANT INSERT ON TABLE audit_logs  TO privshield_audit_writer;
GRANT SELECT ON TABLE audit_logs  TO privshield_audit_writer;
GRANT INSERT ON TABLE snapshots   TO privshield_audit_writer;
GRANT SELECT ON TABLE snapshots   TO privshield_audit_writer;

-- -----------------------------------------------------------------------------
-- 步骤 4：显式撤销改删与清空（对角色、对 PUBLIC 各一次）
-- PostgreSQL 的权限是「累积授予」，仅靠「没给」不足以防住实例级预置授权或
-- 角色成员关系带入的权限，因此这里对 UPDATE/DELETE/TRUNCATE 做显式 REVOKE。
-- 注意：属主隐含全部权限，REVOKE 对属主无效——所以本脚本要求存证服务的连接串
-- 使用该角色，而不是任何属主账号；has_table_privilege 自检正是用来兜住这一点。
-- -----------------------------------------------------------------------------
REVOKE UPDATE, DELETE, TRUNCATE ON TABLE audit_logs  FROM privshield_audit_writer;
REVOKE UPDATE, DELETE, TRUNCATE ON TABLE snapshots   FROM privshield_audit_writer;
REVOKE UPDATE, DELETE, TRUNCATE ON TABLE audit_logs  FROM PUBLIC;
REVOKE UPDATE, DELETE, TRUNCATE ON TABLE snapshots   FROM PUBLIC;

-- 快照表的 ON DELETE CASCADE 依赖 audit_logs 的 DELETE 权限，只写角色下永远不会触发；
-- 这里再撤销 snapshots 的 REFERENCES，避免该角色通过「给别的表建外键指向它」间接改数据。
REVOKE REFERENCES ON TABLE audit_logs  FROM privshield_audit_writer;
REVOKE REFERENCES ON TABLE snapshots   FROM privshield_audit_writer;
REVOKE TRIGGER ON TABLE audit_logs  FROM privshield_audit_writer;
REVOKE TRIGGER ON TABLE snapshots   FROM privshield_audit_writer;
-- 该角色不得创建临时表（TEMPORARY 为库级权限，PG 默认授给 PUBLIC）—— 撤销动作在步骤 2
-- 的 DO 块里随库名一起完成，因为 ON DATABASE 必须写明数据库名。
-- PG17+ 起可再加回 MAINTAIN（阻止该角色 VACUUM/ANALYZE 存证表）；PG16 无此权限名，写了会报错。

-- -----------------------------------------------------------------------------
-- 步骤 5：自检（与应用启动自检同口径；期望四行全部为 false）
-- 任意为 true 说明该角色已是属主或被别处重新授予了改删权限，服务将拒绝启动。
-- -----------------------------------------------------------------------------
SELECT
    has_table_privilege('privshield_audit_writer', 'audit_logs', 'INSERT')  AS audit_logs_insert_ok,
    has_table_privilege('privshield_audit_writer', 'audit_logs', 'SELECT')  AS audit_logs_select_ok,
    has_table_privilege('privshield_audit_writer', 'audit_logs', 'UPDATE')  AS audit_logs_update_must_be_false,
    has_table_privilege('privshield_audit_writer', 'audit_logs', 'DELETE')  AS audit_logs_delete_must_be_false,
    has_table_privilege('privshield_audit_writer', 'snapshots',  'UPDATE')  AS snapshots_update_must_be_false,
    has_table_privilege('privshield_audit_writer', 'snapshots',  'DELETE')  AS snapshots_delete_must_be_false,
    has_table_privilege('privshield_audit_writer', 'audit_logs', 'TRUNCATE') AS audit_logs_truncate_must_be_false;

-- 以只写角色亲自复核一遍（应看到 42501 permission denied for table audit_logs）：
--   psql "postgresql://privshield_audit_writer@<host>:5432/<db>?sslmode=require" \
--        -c "UPDATE audit_logs SET status='tampered' WHERE id='nope'"
--
-- 若之后执行了 deploy/sql/audit_partition.sql 做分区迁移，**必须重跑本脚本**：
-- 表被整体换成了新对象，权限不随重命名转移；分区父表上的授权会自动覆盖其下所有
-- 分区（含未来新建分区），无需逐分区 GRANT。
