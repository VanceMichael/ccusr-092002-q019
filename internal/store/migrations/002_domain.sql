-- 高坝鱼类过坝链路核验：领域表结构
-- 环节顺序：集鱼槽 COLLECTION -> 分拣 SORTING -> 155 米轨道提升 RAIL_LIFT
--          -> 无人车陆运 LAND_TRANSPORT -> 运鱼船 VESSEL_TRANSPORT -> 放流点 RELEASE_SITE -> 放流

-- 本土鱼类参考名录（仅用于提示，登记批次时不强制外键，避免新物种无法录入）
CREATE TABLE IF NOT EXISTS species (
    code            TEXT PRIMARY KEY,
    common_name     TEXT NOT NULL,
    scientific_name TEXT NOT NULL
);
INSERT OR IGNORE INTO species (code, common_name, scientific_name) VALUES
    ('S_WANG',  '短须裂腹鱼', 'Schizothorax wangchiachii'),
    ('S_DOLI',  '长丝裂腹鱼', 'Schizothorax dolichonema'),
    ('S_CHON',  '细鳞裂腹鱼', 'Schizothorax chongi'),
    ('S_KOZL',  '四川裂腹鱼', 'Schizothorax kozlovi'),
    ('P_PING',  '鲈鲤',       'Percocypris pingi'),
    ('L_ELON',  '长薄鳅',     'Leptobotia elongata'),
    ('P_RABA',  '岩原鲤',     'Procypris rabaudi');

-- 鱼批次：origin 区分天然过坝野生鱼（WILD）与增殖站鱼苗（HATCHERY），
-- 只有 HATCHERY 批次的放流才计入增殖放流成效。
CREATE TABLE IF NOT EXISTS lots (
    lot_ref           TEXT PRIMARY KEY,
    species_code      TEXT NOT NULL,
    origin            TEXT NOT NULL CHECK (origin IN ('WILD', 'HATCHERY')),
    initial_count     INTEGER NOT NULL DEFAULT 0 CHECK (initial_count >= 0),
    entry_stage       TEXT NOT NULL,
    current_stage     TEXT NOT NULL,
    status            TEXT NOT NULL CHECK (status IN ('IN_CHAIN', 'IN_TRANSIT', 'BLOCKED', 'RELEASED')) DEFAULT 'IN_CHAIN',
    created_by_resort INTEGER NOT NULL DEFAULT 0,
    notes             TEXT,
    created_at        TEXT NOT NULL
);

-- 逐段交接（箱体随交接单一起登记在 handover_containers）
CREATE TABLE IF NOT EXISTS handovers (
    handover_id    TEXT PRIMARY KEY,
    lot_ref        TEXT NOT NULL REFERENCES lots(lot_ref),
    seq            INTEGER NOT NULL,
    from_stage     TEXT NOT NULL,
    to_stage       TEXT NOT NULL,
    sent_count     INTEGER NOT NULL CHECK (sent_count >= 0),
    received_count INTEGER,
    status         TEXT NOT NULL CHECK (status IN ('SENT', 'CONFIRMED', 'DISPUTED')),
    discrepancy    INTEGER,
    sent_at        TEXT NOT NULL,
    received_at    TEXT,
    sent_by        TEXT,
    received_by    TEXT,
    note           TEXT,
    recorded_at    TEXT NOT NULL,
    UNIQUE (lot_ref, seq)
);
CREATE INDEX IF NOT EXISTS idx_handovers_lot ON handovers(lot_ref, seq);

CREATE TABLE IF NOT EXISTS handover_containers (
    handover_id    TEXT NOT NULL REFERENCES handovers(handover_id) ON DELETE CASCADE,
    container_ref  TEXT NOT NULL,
    sent_count     INTEGER NOT NULL CHECK (sent_count >= 0),
    received_count INTEGER,
    PRIMARY KEY (handover_id, container_ref)
);

CREATE TABLE IF NOT EXISTS containers (
    container_ref TEXT PRIMARY KEY,
    first_seen_at TEXT NOT NULL
);

-- 死亡记录：只有 CONFIRMED 才能覆盖交接数量差额
CREATE TABLE IF NOT EXISTS loss_events (
    loss_id     TEXT PRIMARY KEY,
    lot_ref     TEXT NOT NULL REFERENCES lots(lot_ref),
    handover_id TEXT REFERENCES handovers(handover_id),
    stage_code  TEXT NOT NULL,
    count       INTEGER NOT NULL CHECK (count > 0),
    cause       TEXT NOT NULL,
    status      TEXT NOT NULL CHECK (status IN ('PENDING', 'CONFIRMED', 'REJECTED')) DEFAULT 'PENDING',
    fault_id    TEXT REFERENCES equipment_faults(fault_id),
    occurred_at TEXT NOT NULL,
    recorded_at TEXT NOT NULL,
    confirmed_at TEXT,
    recorded_by  TEXT,
    confirmed_by TEXT,
    notes        TEXT
);
CREATE INDEX IF NOT EXISTS idx_losses_lot ON loss_events(lot_ref);
CREATE INDEX IF NOT EXISTS idx_losses_handover ON loss_events(handover_id);

-- 重新分拣：把鱼从一个批次划到同阶段的另一批次（常见于物种复核）
-- 挂在 handover_id 上的重分拣用于销记交接短少差额。
CREATE TABLE IF NOT EXISTS resort_events (
    resort_id    TEXT PRIMARY KEY,
    from_lot_ref TEXT NOT NULL REFERENCES lots(lot_ref),
    to_lot_ref   TEXT NOT NULL REFERENCES lots(lot_ref),
    handover_id  TEXT REFERENCES handovers(handover_id),
    stage_code   TEXT NOT NULL,
    count        INTEGER NOT NULL CHECK (count > 0),
    reason       TEXT,
    status       TEXT NOT NULL CHECK (status IN ('PENDING', 'CONFIRMED', 'REJECTED')) DEFAULT 'PENDING',
    occurred_at  TEXT NOT NULL,
    recorded_at  TEXT NOT NULL,
    confirmed_at TEXT,
    recorded_by  TEXT,
    confirmed_by TEXT
);
CREATE INDEX IF NOT EXISTS idx_resorts_from ON resort_events(from_lot_ref);
CREATE INDEX IF NOT EXISTS idx_resorts_to ON resort_events(to_lot_ref);

-- 设备故障（轨道提升机、无人车、运鱼船等），即使零死亡也要留痕
CREATE TABLE IF NOT EXISTS equipment_faults (
    fault_id    TEXT PRIMARY KEY,
    equipment_ref TEXT NOT NULL,
    stage_code  TEXT NOT NULL,
    fault_type  TEXT NOT NULL,
    description TEXT,
    status      TEXT NOT NULL CHECK (status IN ('OPEN', 'RESOLVED')) DEFAULT 'OPEN',
    started_at  TEXT NOT NULL,
    resolved_at TEXT,
    recorded_by TEXT,
    recorded_at TEXT NOT NULL,
    notes       TEXT
);
CREATE TABLE IF NOT EXISTS fault_lots (
    fault_id TEXT NOT NULL REFERENCES equipment_faults(fault_id) ON DELETE CASCADE,
    lot_ref  TEXT NOT NULL REFERENCES lots(lot_ref),
    PRIMARY KEY (fault_id, lot_ref)
);

-- 放流（一批一次），记录放流地点
CREATE TABLE IF NOT EXISTS releases (
    release_id  TEXT PRIMARY KEY,
    lot_ref     TEXT NOT NULL UNIQUE REFERENCES lots(lot_ref),
    release_ref TEXT,
    count       INTEGER NOT NULL CHECK (count >= 0),
    waterbody   TEXT NOT NULL,
    site_name   TEXT,
    latitude    REAL,
    longitude   REAL,
    released_at TEXT NOT NULL,
    released_by TEXT,
    notes       TEXT,
    recorded_at TEXT NOT NULL
);

-- 耳石 / 荧光等标记，mark_code 供回捕调查匹配
CREATE TABLE IF NOT EXISTS marks (
    mark_id      TEXT PRIMARY KEY,
    lot_ref      TEXT NOT NULL REFERENCES lots(lot_ref),
    mark_type    TEXT NOT NULL CHECK (mark_type IN ('OTOLITH', 'FLUORESCENT', 'PIT', 'OTHER')),
    mark_code    TEXT NOT NULL UNIQUE,
    marked_count INTEGER NOT NULL CHECK (marked_count > 0),
    marked_at    TEXT NOT NULL,
    method       TEXT,
    notes        TEXT,
    recorded_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_marks_lot ON marks(lot_ref);

-- 回捕调查
CREATE TABLE IF NOT EXISTS surveys (
    survey_id        TEXT PRIMARY KEY,
    survey_ref       TEXT,
    survey_date      TEXT NOT NULL,
    waterbody        TEXT NOT NULL,
    site_name        TEXT,
    latitude         REAL,
    longitude        REAL,
    method           TEXT,
    investigator_ref TEXT,
    notes            TEXT,
    recorded_at      TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS recaptures (
    recapture_id   TEXT PRIMARY KEY,
    survey_id      TEXT NOT NULL REFERENCES surveys(survey_id) ON DELETE CASCADE,
    mark_code      TEXT,
    species_code   TEXT NOT NULL,
    count          INTEGER NOT NULL CHECK (count > 0),
    match_status   TEXT NOT NULL CHECK (match_status IN ('MATCHED', 'UNMATCHED')),
    matched_mark_id TEXT REFERENCES marks(mark_id),
    matched_lot_ref TEXT REFERENCES lots(lot_ref),
    notes          TEXT
);
CREATE INDEX IF NOT EXISTS idx_recaptures_lot ON recaptures(matched_lot_ref);

INSERT OR IGNORE INTO schema_migrations(version) VALUES ('002_domain');
