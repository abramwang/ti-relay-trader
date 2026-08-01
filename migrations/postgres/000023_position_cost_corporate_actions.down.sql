ALTER TABLE performance_position_cost_states
    DROP CONSTRAINT IF EXISTS performance_position_cost_states_corporate_action_factor_check,
    DROP CONSTRAINT IF EXISTS performance_position_cost_states_open_quantity_check,
    DROP COLUMN IF EXISTS corporate_action_context,
    DROP COLUMN IF EXISTS corporate_action_source,
    DROP COLUMN IF EXISTS corporate_action_quantity_delta,
    DROP COLUMN IF EXISTS corporate_action_factor,
    DROP COLUMN IF EXISTS corporate_action_type,
    DROP COLUMN IF EXISTS broker_open_quantity,
    DROP COLUMN IF EXISTS previous_close_quantity;
