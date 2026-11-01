-- Railway Revamped: Sovereign Distributed SQL Schema (CockroachDB / PostgreSQL)
-- Document Reference: ENG-ARCH-RR-2026-01 / PRD-RR-2026-01
-- Multi-Region Quorum & Relational Guarantee (0.00% Physical Double-Bookings)

CREATE TABLE IF NOT EXISTS stations (
    code VARCHAR(10) PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    catchment_cluster VARCHAR(50), -- e.g. 'DELHI', 'MUMBAI', 'KOLKATA'
    zone VARCHAR(10) NOT NULL,       -- e.g. 'NR', 'WR', 'ER', 'SR', 'ECR'
    division VARCHAR(50) NOT NULL,
    latitude DECIMAL(9, 6),
    longitude DECIMAL(9, 6),
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS trains (
    train_number VARCHAR(10) PRIMARY KEY,
    train_name VARCHAR(120) NOT NULL,
    train_type VARCHAR(30) NOT NULL, -- 'VANDE_BHARAT', 'RAJDHANI', 'SHATABDI', 'SUPERFAST', 'EXPRESS'
    origin_station_code VARCHAR(10) NOT NULL REFERENCES stations(code),
    dest_station_code VARCHAR(10) NOT NULL REFERENCES stations(code),
    total_stops INT NOT NULL,
    is_active BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS train_route_stations (
    train_number VARCHAR(10) NOT NULL REFERENCES trains(train_number),
    stop_index SMALLINT NOT NULL, -- 0 to N-1
    station_code VARCHAR(10) NOT NULL REFERENCES stations(code),
    distance_km INT NOT NULL,
    arrival_time TIME,
    departure_time TIME,
    day_offset SMALLINT DEFAULT 0,
    PRIMARY KEY (train_number, stop_index)
);

CREATE TABLE IF NOT EXISTS train_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    train_number VARCHAR(10) NOT NULL REFERENCES trains(train_number),
    run_date DATE NOT NULL,
    scheduled_departure TIMESTAMPTZ NOT NULL,
    status VARCHAR(20) DEFAULT 'SCHEDULED', -- 'SCHEDULED', 'CHARTED', 'DEPARTED', 'CANCELLED', 'DIVERTED'
    charting_status VARCHAR(20) DEFAULT 'PENDING', -- 'PENDING', 'CHART_PREPARED', 'FINALIZED'
    charting_time TIMESTAMPTZ, -- Set at T-8 hours automatically
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_train_run UNIQUE (train_number, run_date)
);

CREATE TABLE IF NOT EXISTS coaches (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    train_run_id UUID NOT NULL REFERENCES train_runs(id) ON DELETE RESTRICT,
    coach_id VARCHAR(10) NOT NULL, -- e.g. 'B1', 'B2', 'A1', 'C1'
    class_code VARCHAR(10) NOT NULL, -- '1A', '2A', '3A', '3E', 'SL', 'CC', 'EC'
    total_berths SMALLINT NOT NULL,
    bay_count SMALLINT NOT NULL,
    display_order SMALLINT NOT NULL,
    CONSTRAINT uq_run_coach UNIQUE (train_run_id, coach_id)
);

-- Core Relational Invariant: Zero Physical Double Bookings
-- Composite primary key strictly prevents any two bookings from holding the same berth on the same station segment.
CREATE TABLE IF NOT EXISTS berth_allocations (
    train_run_id UUID NOT NULL REFERENCES train_runs(id) ON DELETE RESTRICT,
    coach_id VARCHAR(10) NOT NULL,
    berth_number SMALLINT NOT NULL,
    segment_index SMALLINT NOT NULL, -- Individual station hop i -> i+1
    pnr_number VARCHAR(10) NOT NULL,
    quota_code VARCHAR(10) NOT NULL, -- 'GN', 'TQ', 'PT', 'SS', 'LD'
    status VARCHAR(15) NOT NULL,     -- 'HELD', 'CONFIRMED'
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (train_run_id, coach_id, berth_number, segment_index)
);

CREATE INDEX IF NOT EXISTS idx_berth_allocations_pnr ON berth_allocations (pnr_number);
CREATE INDEX IF NOT EXISTS idx_berth_allocations_run ON berth_allocations (train_run_id, coach_id);

CREATE TABLE IF NOT EXISTS pnrs (
    pnr_number VARCHAR(10) PRIMARY KEY,
    train_run_id UUID NOT NULL REFERENCES train_runs(id) ON DELETE RESTRICT,
    user_id VARCHAR(64) NOT NULL,
    origin_stop_index SMALLINT NOT NULL,
    dest_stop_index SMALLINT NOT NULL,
    segment_bitmask BIGINT NOT NULL,
    class_code VARCHAR(10) NOT NULL,
    quota_code VARCHAR(10) NOT NULL,
    status VARCHAR(20) NOT NULL, -- 'INITIALIZED', 'HELD', 'PAYMENT_PENDING', 'CONFIRMED', 'CANCELLED', 'EXPIRED', 'TRAVELLED'
    total_fare DECIMAL(10, 2) NOT NULL,
    lease_expires_at TIMESTAMPTZ,
    has_flexi_cancel BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_pnrs_user ON pnrs(user_id);
CREATE INDEX IF NOT EXISTS idx_pnrs_train_run ON pnrs(train_run_id);

CREATE TABLE IF NOT EXISTS passengers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pnr_number VARCHAR(10) NOT NULL REFERENCES pnrs(pnr_number) ON DELETE CASCADE,
    full_name VARCHAR(100) NOT NULL,
    age SMALLINT NOT NULL,
    gender VARCHAR(10) NOT NULL,
    senior_citizen BOOLEAN DEFAULT FALSE,
    berth_preference VARCHAR(10), -- 'LOWER', 'MIDDLE', 'UPPER', 'SIDE_LOWER', 'SIDE_UPPER', 'NO_PREF'
    booking_status VARCHAR(15) NOT NULL, -- 'CNF', 'RAC', 'WL'
    allocated_coach VARCHAR(10),
    allocated_berth SMALLINT,
    allocated_berth_type VARCHAR(10), -- 'LB', 'MB', 'UB', 'SL', 'SU'
    rac_or_wl_number INT,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS payment_transactions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pnr_number VARCHAR(10) NOT NULL REFERENCES pnrs(pnr_number),
    user_id VARCHAR(64) NOT NULL,
    amount_inr DECIMAL(10, 2) NOT NULL,
    gateway VARCHAR(30) NOT NULL, -- 'RAZORPAY', 'SBI_EPAY', 'HDFC_PG', 'CASHFREE'
    gateway_txn_id VARCHAR(100),
    authorization_token VARCHAR(120),
    status VARCHAR(20) NOT NULL, -- 'PRE_AUTH_INIT', 'AUTHORIZED', 'CAPTURED', 'FAILED', 'VOIDED', 'REFUND_QUEUED', 'REFUNDED'
    lease_token VARCHAR(64) NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS transit_ledger (
    user_id VARCHAR(64) PRIMARY KEY,
    balance_inr DECIMAL(10, 2) DEFAULT 0.00 CHECK (balance_inr >= 0.00),
    last_updated TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS transit_ledger_entries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id VARCHAR(64) NOT NULL REFERENCES transit_ledger(user_id),
    pnr_number VARCHAR(10),
    amount_inr DECIMAL(10, 2) NOT NULL,
    entry_type VARCHAR(20) NOT NULL, -- 'INSTANT_REFUND', 'BOOKING_PAYMENT', 'TOPUP', 'WITHDRAWAL'
    note TEXT,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
