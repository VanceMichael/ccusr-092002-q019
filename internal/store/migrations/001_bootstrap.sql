-- 高坝鱼类过坝链路核验：批次、逐段交接、守恒凭证、放流与回捕监测

CREATE TABLE IF NOT EXISTS schema_migrations (
    version    TEXT PRIMARY KEY,
    applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- 鱼批：野生过坝鱼与增殖放流鱼苗分账（origin），绝不混计成效
CREATE TABLE IF NOT EXISTS lots (
    lot_code       TEXT PRIMARY KEY,              -- 业务批次号，对应交换字段 fish_lot
    species_code   TEXT NOT NULL,                 -- 物种代码，如 SCHIZOTHORAX
    origin         TEXT NOT NULL CHECK (origin IN ('WILD', 'HATCHERY')),
    source         TEXT NOT NULL DEFAULT '',      -- 集鱼点 / 增殖站
    initial_count  INTEGER NOT NULL CHECK (initial_count >= 0),
    start_stage    TEXT NOT NULL DEFAULT 'SORTING', -- 重新分拣另立的批次可从中途环节进入链路
    status         TEXT NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'RELEASED', 'CLOSED')),
    note           TEXT NOT NULL DEFAULT '',
    recorded_by    TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL
);

-- 五个固定环节（顺序由应用层强制）：
-- SORTING 分拣 → HOIST_155M 155米轨道提升 → LAND_TRANSPORT 陆运(无人车)
-- → VESSEL_TRANSPORT 船运(运鱼船) → RELEASE 放流
CREATE TABLE IF NOT EXISTS handoffs (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    lot_code          TEXT NOT NULL REFERENCES lots(lot_code),
    stage_code        TEXT NOT NULL,
    seq               INTEGER NOT NULL,                 -- 该批次内的环节序号，从 1 开始
    expected_count    INTEGER NOT NULL CHECK (expected_count >= 0),
    received_count    INTEGER,                          -- 接收清点前为空
    shortage_count    INTEGER,                          -- expected - received
    status            TEXT NOT NULL DEFAULT 'DISPATCHED'
                        CHECK (status IN ('DISPATCHED', 'BLOCKED', 'BALANCED')),
    reported_by       TEXT NOT NULL DEFAULT '',
    receiver_by       TEXT NOT NULL DEFAULT '',
    note              TEXT NOT NULL DEFAULT '',
    created_at        TEXT NOT NULL,
    received_at       TEXT,
    -- 放流环节专有字段
    release_site_code TEXT NOT NULL DEFAULT '',
    release_site_name TEXT NOT NULL DEFAULT '',
    longitude         REAL,
    latitude          REAL,
    released_at       TEXT,
    UNIQUE (lot_code, stage_code)
);

-- 运输箱逐箱交接清单
CREATE TABLE IF NOT EXISTS handoff_containers (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    handoff_id     INTEGER NOT NULL REFERENCES handoffs(id) ON DELETE CASCADE,
    container_ref  TEXT NOT NULL,        -- 对应交换字段 container_ref
    expected_count INTEGER NOT NULL CHECK (expected_count >= 0),
    received_count INTEGER,
    UNIQUE (handoff_id, container_ref)
);

-- 短数凭证：死亡或重新分拣，须经确认后才具备守恒效力
CREATE TABLE IF NOT EXISTS loss_events (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    handoff_id    INTEGER NOT NULL REFERENCES handoffs(id),
    lot_code      TEXT NOT NULL REFERENCES lots(lot_code),
    stage_code    TEXT NOT NULL,            -- 冗余自 handoffs.stage_code，便于按环节汇总
    kind          TEXT NOT NULL CHECK (kind IN ('DEATH', 'RESORT')),
    count         INTEGER NOT NULL CHECK (count > 0),
    to_lot_code   TEXT REFERENCES lots(lot_code),  -- 仅 RESORT：鱼被重新分拣并入的目标批次
    reason        TEXT NOT NULL DEFAULT '',
    evidence_ref  TEXT NOT NULL DEFAULT '',        -- 照片 / 录像 / 纸质单据编号
    status        TEXT NOT NULL DEFAULT 'PENDING'
                    CHECK (status IN ('PENDING', 'CONFIRMED', 'REJECTED')),
    reported_by   TEXT NOT NULL DEFAULT '',
    confirmed_by  TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL,
    confirmed_at  TEXT
);

-- 批次数量调整（重新分拣在两个批次间留下双向痕迹）
CREATE TABLE IF NOT EXISTS lot_adjustments (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    lot_code            TEXT NOT NULL REFERENCES lots(lot_code),
    delta               INTEGER NOT NULL,       -- RESORT_IN 为正，RESORT_OUT 为负
    reason_kind         TEXT NOT NULL CHECK (reason_kind IN ('RESORT_IN', 'RESORT_OUT')),
    ref_loss_event_id   INTEGER NOT NULL REFERENCES loss_events(id),
    stage_code          TEXT NOT NULL,
    created_at          TEXT NOT NULL
);

-- 设备故障登记（集鱼设施 / 提升机 / 无人车 / 运鱼船等），故障本身不能抵销短数
CREATE TABLE IF NOT EXISTS equipment_failures (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    lot_code        TEXT REFERENCES lots(lot_code),  -- 可只登记设备、不绑定具体批次
    stage_code      TEXT NOT NULL,
    equipment_code  TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    downtime_minutes INTEGER,
    occurred_at     TEXT NOT NULL,
    reported_by     TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL
);

-- 增殖鱼苗标记：耳石（热标记）或荧光标记，仅允许挂在 HATCHERY 批次
CREATE TABLE IF NOT EXISTS marks (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    lot_code     TEXT NOT NULL REFERENCES lots(lot_code),
    mark_type    TEXT NOT NULL CHECK (mark_type IN ('OTOLITH', 'FLUORESCENT')),
    marker_code  TEXT NOT NULL,            -- 标记批号 / 荧光色号 / 耳石纹理解码字
    marked_count INTEGER NOT NULL CHECK (marked_count > 0),
    marked_at    TEXT NOT NULL,
    recorded_by  TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    UNIQUE (mark_type, marker_code)
);

-- 放流后的回捕调查（数月后监测证据的载体）
CREATE TABLE IF NOT EXISTS surveys (
    survey_code   TEXT PRIMARY KEY,
    survey_date   TEXT NOT NULL,           -- ISO 8601 日期或带偏移时间
    site_code     TEXT NOT NULL DEFAULT '',
    site_name     TEXT NOT NULL,
    longitude     REAL,
    latitude      REAL,
    method        TEXT NOT NULL DEFAULT '',
    investigators TEXT NOT NULL DEFAULT '',
    note          TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL
);

-- 回捕检出：只有标记命中已登记增殖标记才计入放流成效；无标记天然鱼记 WILD
CREATE TABLE IF NOT EXISTS recaptures (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    survey_code         TEXT NOT NULL REFERENCES surveys(survey_code),
    species_code        TEXT NOT NULL,
    count               INTEGER NOT NULL CHECK (count > 0),
    mark_type           TEXT CHECK (mark_type IS NULL OR mark_type IN ('OTOLITH', 'FLUORESCENT')),
    marker_code         TEXT NOT NULL DEFAULT '',
    origin_result       TEXT NOT NULL
                          CHECK (origin_result IN ('CREDITED', 'WILD', 'UNMATCHED_MARK')),
    matched_lot_code    TEXT REFERENCES lots(lot_code),
    evidence_ref        TEXT NOT NULL DEFAULT '',
    created_at          TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_handoffs_lot           ON handoffs(lot_code);
CREATE INDEX IF NOT EXISTS idx_containers_handoff     ON handoff_containers(handoff_id);
CREATE INDEX IF NOT EXISTS idx_losses_handoff         ON loss_events(handoff_id);
CREATE INDEX IF NOT EXISTS idx_losses_lot             ON loss_events(lot_code);
CREATE INDEX IF NOT EXISTS idx_adjustments_lot        ON lot_adjustments(lot_code);
CREATE INDEX IF NOT EXISTS idx_failures_lot_stage     ON equipment_failures(lot_code, stage_code);
CREATE INDEX IF NOT EXISTS idx_marks_lot              ON marks(lot_code);
CREATE INDEX IF NOT EXISTS idx_recaptures_survey      ON recaptures(survey_code);
CREATE INDEX IF NOT EXISTS idx_recaptures_lot         ON recaptures(matched_lot_code);
