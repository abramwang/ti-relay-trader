DO $$ BEGIN
    IF EXISTS(SELECT 1 FROM securities_command_intents) THEN
        RAISE EXCEPTION 'registered securities commands exist; downgrade refused';
    END IF;
END $$;
DROP TABLE securities_command_publications;
DROP TABLE securities_command_dispatches;
DROP TABLE securities_command_intents;
