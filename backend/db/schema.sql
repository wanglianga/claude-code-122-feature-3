-- 精酿酒吧空桶回收押金与批次追溯平台 - 数据库结构
-- PostgreSQL 13+

CREATE TABLE IF NOT EXISTS app_users (
    id           BIGSERIAL PRIMARY KEY,
    username     TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL,
    role         TEXT NOT NULL CHECK (role IN ('brewery','bar_manager','driver','washplant','qc','finance','admin','dealer')),
    org_id       BIGINT,
    password_sha TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS cities (
    id   BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL UNIQUE
);

CREATE TABLE IF NOT EXISTS dealers (
    id         BIGSERIAL PRIMARY KEY,
    city_id    BIGINT NOT NULL REFERENCES cities(id),
    name       TEXT NOT NULL,
    contact    TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 酒吧既可能直属于酒厂，也可能挂在经销商名下（多城市经销商）
CREATE TABLE IF NOT EXISTS bars (
    id         BIGSERIAL PRIMARY KEY,
    city_id    BIGINT NOT NULL REFERENCES cities(id),
    dealer_id  BIGINT REFERENCES dealers(id),
    name       TEXT NOT NULL,
    address    TEXT,
    manager    TEXT,
    phone      TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS drivers (
    id      BIGSERIAL PRIMARY KEY,
    city_id BIGINT NOT NULL REFERENCES cities(id),
    name    TEXT NOT NULL,
    phone   TEXT
);

CREATE TABLE IF NOT EXISTS washplants (
    id      BIGSERIAL PRIMARY KEY,
    city_id BIGINT NOT NULL REFERENCES cities(id),
    name    TEXT NOT NULL
);

-- 酒款：IPA / 世涛 / 小麦 / 酸啤 ...
CREATE TABLE IF NOT EXISTS beers (
    id        BIGSERIAL PRIMARY KEY,
    name      TEXT NOT NULL,
    style     TEXT NOT NULL CHECK (style IN ('IPA','世涛','小麦','酸啤')),
    abv       NUMERIC(4,2) NOT NULL DEFAULT 0,
    active    BOOLEAN NOT NULL DEFAULT TRUE
);

CREATE TABLE IF NOT EXISTS batches (
    id                  BIGSERIAL PRIMARY KEY,
    beer_id             BIGINT NOT NULL REFERENCES beers(id),
    batch_no            TEXT NOT NULL UNIQUE,            -- 酒液批次号
    brewed_date         DATE NOT NULL,                  -- 酿造日期
    filled_date         DATE,                           -- 灌装日期
    best_before_days    INT NOT NULL DEFAULT 30,        -- 最佳饮用期（灌装后天数）
    total_kegs          INT NOT NULL DEFAULT 0,
    -- NORMAL 正常 / SUSPENDED 暂停销售 / RECALLED 召回 / CLEARED 解除
    status              TEXT NOT NULL DEFAULT 'NORMAL'
        CHECK (status IN ('NORMAL','SUSPENDED','RECALLED','CLEARED')),
    suspend_reason      TEXT,
    suspended_at        TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 酒桶资产台账
CREATE TABLE IF NOT EXISTS kegs (
    id              BIGSERIAL PRIMARY KEY,
    keg_code        TEXT NOT NULL UNIQUE,       -- 桶身编号/二维码（KEG-0001）
    capacity_liters NUMERIC(6,2) NOT NULL,      -- 容量（20L/30L/50L）
    tare_weight_kg  NUMERIC(6,2),
    -- 资产状态：IN_STOCK 酒厂仓库 / IN_TRANSIT 配送中 / AT_BAR 在店 / EMPTY_RESERVED 空桶待回收
    --           PICKED 司机已收 / AT_WASH 在洗桶厂 / WASH_FAIL 清洗不合格 / REPAIR 不合格返修中
    --           LOST 丢失 / SCRAPPED 报废
    asset_status    TEXT NOT NULL DEFAULT 'IN_STOCK'
        CHECK (asset_status IN ('IN_STOCK','IN_TRANSIT','AT_BAR','EMPTY_RESERVED','PICKED',
                                'AT_WASH','WASH_FAIL','REPAIR','LOST','SCRAPPED')),
    refillable      BOOLEAN NOT NULL DEFAULT TRUE,  -- 再灌装资格
    current_bar_id  BIGINT REFERENCES bars(id),
    current_cycle_id BIGINT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 发货订单：一次配送包含多个桶（可标注节日临时铺货）
CREATE TABLE IF NOT EXISTS orders (
    id              BIGSERIAL PRIMARY KEY,
    order_no        TEXT NOT NULL UNIQUE,
    bar_id          BIGINT NOT NULL REFERENCES bars(id),
    dealer_id       BIGINT REFERENCES dealers(id),
    driver_id       BIGINT REFERENCES drivers(id),
    city_id         BIGINT NOT NULL REFERENCES cities(id),
    is_festival     BOOLEAN NOT NULL DEFAULT FALSE,  -- 节日临时铺货
    festival_note   TEXT,
    deposit_term_days INT NOT NULL DEFAULT 30,       -- 酒桶押金账期（天）
    status          TEXT NOT NULL DEFAULT 'PLANNED'
        CHECK (status IN ('PLANNED','DELIVERING','DELIVERED','CANCELLED')),
    created_by      BIGINT REFERENCES app_users(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    delivered_at    TIMESTAMPTZ
);

-- 订单明细：PLANNED 阶段仅记录"想要哪个批次多少桶"，发货时才分配具体酒桶
CREATE TABLE IF NOT EXISTS order_items (
    id        BIGSERIAL PRIMARY KEY,
    order_id  BIGINT NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    batch_id  BIGINT NOT NULL REFERENCES batches(id),
    keg_id    BIGINT REFERENCES kegs(id),
    qty       INT NOT NULL DEFAULT 1
);

-- 桶流转周期：一只桶一次"灌装->在店->回收->清洗->回库"的完整生命段，
-- 酒厂/酒吧/司机/清洗厂/品控/财务全部围绕同一 cycle_id 协同
CREATE TABLE IF NOT EXISTS keg_cycles (
    id               BIGSERIAL PRIMARY KEY,
    keg_id           BIGINT NOT NULL REFERENCES kegs(id),
    batch_id         BIGINT NOT NULL REFERENCES batches(id),
    order_id         BIGINT REFERENCES orders(id),
    bar_id           BIGINT REFERENCES bars(id),
    driver_id        BIGINT REFERENCES drivers(id),
    city_id          BIGINT REFERENCES cities(id),
    deposit_amount   NUMERIC(10,2) NOT NULL,        -- 押金金额
    deposit_due_date DATE NOT NULL,                 -- 押金账期到期日
    -- 周期阶段：DELIVERED 已配送在店 / TAPPED 已开桶 / EMPTY 已打空
    --           RESERVED 已预约回收 / RETURNED 已回收 / WASHED 清洗合格 / FAILED 清洗不合格
    --           REPAIRING 不合格返修中（返修经酒厂仓库复检确认后回到 WASHED）
    stage            TEXT NOT NULL DEFAULT 'DELIVERED'
        CHECK (stage IN ('DELIVERED','TAPPED','EMPTY','RESERVED','RETURNED','WASHED','FAILED','REPAIRING')),
    tapped_at        TIMESTAMPTZ,
    emptied_at       TIMESTAMPTZ,
    return_reserved_at TIMESTAMPTZ,
    picked_at        TIMESTAMPTZ,
    wash_id          BIGINT,
    -- 收货人/验收
    bar_receiver     TEXT,                          -- 酒吧收货人
    -- 押金：HELD 占用中 / RELEASED 已释放 / DEDUCTED 扣款 / PARTIAL 部分扣款
    deposit_status   TEXT NOT NULL DEFAULT 'HELD'
        CHECK (deposit_status IN ('HELD','RELEASED','DEDUCTED','PARTIAL')),
    released_amount  NUMERIC(10,2) NOT NULL DEFAULT 0,
    deducted_amount  NUMERIC(10,2) NOT NULL DEFAULT 0,
    deduct_reason    TEXT,
    settled_by       BIGINT REFERENCES app_users(id),
    settled_at       TIMESTAMPTZ,
    -- 异常标记
    late_return      BOOLEAN NOT NULL DEFAULT FALSE,    -- 延迟归还
    missing_scan     BOOLEAN NOT NULL DEFAULT FALSE,    -- 司机漏扫
    lost             BOOLEAN NOT NULL DEFAULT FALSE,    -- 酒桶丢失
    deformed         BOOLEAN NOT NULL DEFAULT FALSE,    -- 桶体变形
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE kegs ADD CONSTRAINT fk_kegs_cycle FOREIGN KEY (current_cycle_id)
    REFERENCES keg_cycles(id) DEFERRABLE INITIALLY DEFERRED;

-- 开桶反馈：出酒速度 / 气压 / 口味异常 / 客诉
CREATE TABLE IF NOT EXISTS tap_feedback (
    id            BIGSERIAL PRIMARY KEY,
    cycle_id      BIGINT NOT NULL REFERENCES keg_cycles(id),
    bar_id        BIGINT NOT NULL REFERENCES bars(id),
    reported_by   TEXT,
    pour_speed    TEXT CHECK (pour_speed IN ('正常','偏慢','过快','不出酒') OR pour_speed IS NULL),
    pressure      TEXT CHECK (pressure IN ('正常','气压过高','气压过低','波动') OR pressure IS NULL),
    taste_off     BOOLEAN NOT NULL DEFAULT FALSE,
    taste_note    TEXT,
    complaint     TEXT,                            -- 客诉描述
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 回收预约
CREATE TABLE IF NOT EXISTS return_reservations (
    id          BIGSERIAL PRIMARY KEY,
    cycle_id    BIGINT NOT NULL REFERENCES keg_cycles(id),
    bar_id      BIGINT NOT NULL REFERENCES bars(id),
    pickup_date DATE NOT NULL,
    empty_count INT NOT NULL DEFAULT 1,           -- 空桶数量
    note        TEXT,
    status      TEXT NOT NULL DEFAULT 'WAITING'
        CHECK (status IN ('WAITING','PICKED','MISSED','CANCELLED')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 一张回收预约可覆盖同一酒吧的多只空桶（用于核对司机漏扫）
CREATE TABLE IF NOT EXISTS reservation_cycles (
    reservation_id BIGINT NOT NULL REFERENCES return_reservations(id) ON DELETE CASCADE,
    cycle_id       BIGINT NOT NULL REFERENCES keg_cycles(id),
    PRIMARY KEY (reservation_id, cycle_id)
);

-- 司机扫码回收记录
CREATE TABLE IF NOT EXISTS pickup_scans (
    id            BIGSERIAL PRIMARY KEY,
    cycle_id      BIGINT NOT NULL REFERENCES keg_cycles(id),
    reservation_id BIGINT REFERENCES return_reservations(id),
    driver_id     BIGINT NOT NULL REFERENCES drivers(id),
    scanned_code  TEXT NOT NULL,
    empty_confirmed BOOLEAN NOT NULL DEFAULT TRUE, -- 空桶数量确认
    appearance    TEXT NOT NULL DEFAULT '完好' CHECK (appearance IN ('完好','凹陷','变形','锈蚀')),
    residue_level TEXT NOT NULL DEFAULT '无' CHECK (residue_level IN ('无','少量','明显残液')),
    cap_status    TEXT NOT NULL DEFAULT '已封盖' CHECK (cap_status IN ('已封盖','未封盖','封盖损坏')),
    scanned_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 清洗厂检测
CREATE TABLE IF NOT EXISTS wash_checks (
    id           BIGSERIAL PRIMARY KEY,
    cycle_id     BIGINT NOT NULL REFERENCES keg_cycles(id),
    washplant_id BIGINT NOT NULL REFERENCES washplants(id),
    wash_batch_no TEXT,                            -- 清洗批次号（返修追溯用）
    inspector    TEXT,
    passed       BOOLEAN NOT NULL,
    pressure_ok  BOOLEAN NOT NULL DEFAULT TRUE,
    seal_ok      BOOLEAN NOT NULL DEFAULT TRUE,
    cleanliness_ok BOOLEAN NOT NULL DEFAULT TRUE,
    defect_note  TEXT,
    checked_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE keg_cycles ADD CONSTRAINT fk_cycles_wash FOREIGN KEY (wash_id)
    REFERENCES wash_checks(id) DEFERRABLE INITIALLY DEFERRED;

-- ---------- 清洗不合格返修 ----------

-- 返修单：清洗厂判不合格后发起，平台据此追溯上一家酒吧 / 回收司机 / 清洗批次
CREATE TABLE IF NOT EXISTS keg_repairs (
    id               BIGSERIAL PRIMARY KEY,
    repair_no        TEXT NOT NULL UNIQUE,            -- 返修单号 RP-0001
    keg_id           BIGINT NOT NULL REFERENCES kegs(id),
    cycle_id         BIGINT NOT NULL REFERENCES keg_cycles(id),
    wash_check_id    BIGINT REFERENCES wash_checks(id),
    washplant_id     BIGINT NOT NULL REFERENCES washplants(id),
    -- 追溯快照（返修发起瞬间的责任链路）
    wash_batch_no    TEXT,                            -- 清洗批次
    bar_id           BIGINT REFERENCES bars(id),      -- 上一家酒吧
    driver_id        BIGINT REFERENCES drivers(id),   -- 回收司机
    batch_id         BIGINT REFERENCES batches(id),   -- 桶内酒液批次
    -- 缺陷：异味 / 阀门损坏（也可写其他说明）
    defect_type      TEXT NOT NULL DEFAULT '异味'
        CHECK (defect_type IN ('异味','阀门损坏','变形','其他')),
    defect_note      TEXT,
    -- 费用与押金扣减（登记时同步生成）
    repair_fee       NUMERIC(10,2) NOT NULL DEFAULT 0,    -- 返修费用
    deposit_deduct   NUMERIC(10,2) NOT NULL DEFAULT 0,    -- 押金扣减
    fee_party        TEXT NOT NULL DEFAULT 'BAR'
        CHECK (fee_party IN ('BAR','WASHPLANT')),         -- 费用承担方：酒吧 / 清洗厂
    -- 状态：OPEN 返修中 / SUBMITTED 清洗厂已上传复检待仓库确认
    --       CONFIRMED 仓库确认合格·恢复可灌装 / REJECTED 复检仍不合格 / SCRAPPED 报废
    status           TEXT NOT NULL DEFAULT 'OPEN'
        CHECK (status IN ('OPEN','SUBMITTED','CONFIRMED','REJECTED','SCRAPPED')),
    -- 返修后复检结果（清洗厂返修完成时上传）
    recheck_result   TEXT,
    recheck_inspector TEXT,
    recheck_note     TEXT,
    submitted_at     TIMESTAMPTZ,
    confirmed_by     BIGINT REFERENCES app_users(id),
    confirmed_at     TIMESTAMPTZ,
    confirm_note     TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 桶体照片：登记缺陷与返修复检各阶段上传，仓库长期保留
CREATE TABLE IF NOT EXISTS repair_photos (
    id          BIGSERIAL PRIMARY KEY,
    repair_id   BIGINT NOT NULL REFERENCES keg_repairs(id) ON DELETE CASCADE,
    stage       TEXT NOT NULL DEFAULT 'DEFECT'
        CHECK (stage IN ('DEFECT','RECHECK')),   -- 缺陷取证 / 返修复检
    file_name   TEXT NOT NULL,
    content_type TEXT NOT NULL DEFAULT 'image/png',
    data        BYTEA NOT NULL,
    uploaded_by BIGINT REFERENCES app_users(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 账单条目：返修费用与押金扣减入账。费用进入对应酒吧或清洗厂账单
CREATE TABLE IF NOT EXISTS billing_charges (
    id           BIGSERIAL PRIMARY KEY,
    repair_id    BIGINT NOT NULL REFERENCES keg_repairs(id),
    keg_id       BIGINT NOT NULL REFERENCES kegs(id),
    cycle_id     BIGINT REFERENCES keg_cycles(id),
    charge_type  TEXT NOT NULL CHECK (charge_type IN ('REPAIR_FEE','DEPOSIT_DEDUCT')),
    party_type   TEXT NOT NULL CHECK (party_type IN ('BAR','WASHPLANT')),
    party_id     BIGINT,                              -- bars.id / washplants.id
    bar_id       BIGINT REFERENCES bars(id),          -- 押金扣减对应酒吧（冗余便于查账）
    amount       NUMERIC(10,2) NOT NULL,
    memo         TEXT,
    -- PENDING 待财务确认 / CONFIRMED 已确认入账 / VOID 作废
    status       TEXT NOT NULL DEFAULT 'PENDING'
        CHECK (status IN ('PENDING','CONFIRMED','VOID')),
    created_by   BIGINT REFERENCES app_users(id),
    confirmed_by BIGINT REFERENCES app_users(id),
    confirmed_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_repairs_status ON keg_repairs(status);
CREATE INDEX IF NOT EXISTS idx_repairs_keg ON keg_repairs(keg_id);
CREATE INDEX IF NOT EXISTS idx_repair_photos_repair ON repair_photos(repair_id);
CREATE INDEX IF NOT EXISTS idx_charges_party ON billing_charges(party_type, party_id, status);
CREATE INDEX IF NOT EXISTS idx_charges_repair ON billing_charges(repair_id);

-- 召回/停售通知
CREATE TABLE IF NOT EXISTS notifications (
    id          BIGSERIAL PRIMARY KEY,
    batch_id    BIGINT NOT NULL REFERENCES batches(id),
    audience    TEXT NOT NULL CHECK (audience IN ('BAR','PENDING_ORDER','DEALER_STOCK','ALL')),
    target_id   BIGINT,                 -- bars.id / orders.id / dealers.id，NULL=广播
    title       TEXT NOT NULL,
    body        TEXT NOT NULL,
    ack_by      BIGINT REFERENCES app_users(id),
    ack_at      TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 桶级事件流水（审计/追溯时间线）
CREATE TABLE IF NOT EXISTS keg_events (
    id         BIGSERIAL PRIMARY KEY,
    keg_id     BIGINT NOT NULL REFERENCES kegs(id),
    cycle_id   BIGINT REFERENCES keg_cycles(id),
    event_type TEXT NOT NULL,
    detail     JSONB NOT NULL DEFAULT '{}'::jsonb,
    actor      TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_cycles_batch ON keg_cycles(batch_id);
CREATE INDEX IF NOT EXISTS idx_cycles_bar ON keg_cycles(bar_id);
CREATE INDEX IF NOT EXISTS idx_cycles_stage ON keg_cycles(stage);
CREATE INDEX IF NOT EXISTS idx_feedback_cycle ON tap_feedback(cycle_id);
CREATE INDEX IF NOT EXISTS idx_events_keg ON keg_events(keg_id, created_at);
CREATE INDEX IF NOT EXISTS idx_notif_batch ON notifications(batch_id);
