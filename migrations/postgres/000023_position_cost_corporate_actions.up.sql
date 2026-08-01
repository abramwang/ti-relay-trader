ALTER TABLE performance_position_cost_states
    ADD COLUMN previous_close_quantity BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN broker_open_quantity BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN corporate_action_type TEXT NOT NULL DEFAULT 'none',
    ADD COLUMN corporate_action_factor NUMERIC(20, 12) NOT NULL DEFAULT 0,
    ADD COLUMN corporate_action_quantity_delta BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN corporate_action_source TEXT NOT NULL DEFAULT '',
    ADD COLUMN corporate_action_context JSONB NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE performance_position_cost_states
    ADD CONSTRAINT performance_position_cost_states_open_quantity_check
        CHECK (previous_close_quantity >= 0 AND broker_open_quantity >= 0),
    ADD CONSTRAINT performance_position_cost_states_corporate_action_factor_check
        CHECK (corporate_action_factor >= 0);
