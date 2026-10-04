-- P3: one atomic statement, also safe when invoked directly instead of by
-- RunMigrations. Legacy copies are removed only after every merge succeeds.
DO $$
DECLARE
    r RECORD;
    t TEXT;
    source_schema TEXT;
    existing_id BIGINT;
    existing_at TIMESTAMPTZ;
    next_id BIGINT;
    max_id BIGINT;
    mapped_group_id BIGINT;
    sequence_name TEXT;
    sequence_value BIGINT;
    sequence_called BOOLEAN;
    needs_sequence BOOLEAN;
BEGIN
    CREATE SCHEMA IF NOT EXISTS "user";
    CREATE TABLE IF NOT EXISTS "user".users (
        id BIGINT PRIMARY KEY, username VARCHAR(64), password_hash VARCHAR(256),
        phone VARCHAR(20), email VARCHAR(128), avatar VARCHAR(512), gender SMALLINT,
        bio TEXT, birthday BIGINT, balance NUMERIC(12,6) DEFAULT 0,
        settings JSONB DEFAULT '{}'::JSONB, created_at TIMESTAMPTZ, updated_at TIMESTAMPTZ
    );
    CREATE TABLE IF NOT EXISTS "user".user_devices (
        id BIGINT PRIMARY KEY, user_id BIGINT, device_id VARCHAR(128),
        platform VARCHAR(32) DEFAULT 'web', push_token VARCHAR(512), ip VARCHAR(64),
        location VARCHAR(128), last_active_at TIMESTAMPTZ, created_at TIMESTAMPTZ
    );
    CREATE TABLE IF NOT EXISTS "user".friends (
        id BIGINT PRIMARY KEY, user_id BIGINT NOT NULL, friend_id BIGINT NOT NULL,
        group_id BIGINT DEFAULT 0 NOT NULL, remark VARCHAR(64) DEFAULT '' NOT NULL,
        created_at TIMESTAMPTZ
    );
    CREATE TABLE IF NOT EXISTS "user".friend_groups (
        id BIGINT PRIMARY KEY, user_id BIGINT NOT NULL, name VARCHAR(64) NOT NULL,
        sort_order INTEGER DEFAULT 0 NOT NULL, created_at TIMESTAMPTZ
    );
    CREATE TABLE IF NOT EXISTS "user".friend_requests (
        id BIGINT PRIMARY KEY, from_user_id BIGINT NOT NULL, to_user_id BIGINT NOT NULL,
        message VARCHAR(256) DEFAULT '' NOT NULL, status SMALLINT DEFAULT 0 NOT NULL,
        created_at TIMESTAMPTZ, updated_at TIMESTAMPTZ
    );
    CREATE TABLE IF NOT EXISTS "user".user_blocks (
        id BIGINT PRIMARY KEY, user_id BIGINT NOT NULL, blocked_user_id BIGINT NOT NULL,
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
    );

    -- This is an offline cutover: old services must be stopped. Lock all
    -- participating tables before selecting conflict winners or allocating ids.
    FOREACH source_schema IN ARRAY ARRAY['user', 'friend', 'public'] LOOP
        FOREACH t IN ARRAY ARRAY['users', 'user_devices', 'friends', 'friend_groups', 'friend_requests', 'user_blocks'] LOOP
            IF to_regclass(format('%I.%I', source_schema, t)) IS NOT NULL THEN
                EXECUTE format('LOCK TABLE %I.%I IN ACCESS EXCLUSIVE MODE', source_schema, t);
            END IF;
        END LOOP;
    END LOOP;

    -- Same account id: newest updated_at wins, NULL is oldest, user wins ties.
    -- Different ids are never collapsed by username/phone/email: the final
    -- unique indexes reject ambiguous identities and roll back the migration.
    IF to_regclass('public.users') IS NOT NULL THEN
        EXECUTE $q$
            INSERT INTO "user".users AS u
                (id, username, password_hash, phone, email, avatar, gender, bio,
                 birthday, balance, settings, created_at, updated_at)
            SELECT id, username, password_hash, phone, email, avatar, gender, bio,
                   birthday, balance, settings, created_at, updated_at
              FROM public.users
            ON CONFLICT (id) DO UPDATE
                SET username = EXCLUDED.username, password_hash = EXCLUDED.password_hash,
                    phone = EXCLUDED.phone, email = EXCLUDED.email, avatar = EXCLUDED.avatar,
                    gender = EXCLUDED.gender, bio = EXCLUDED.bio, birthday = EXCLUDED.birthday,
                    balance = EXCLUDED.balance, settings = EXCLUDED.settings,
                    created_at = EXCLUDED.created_at, updated_at = EXCLUDED.updated_at
              WHERE EXCLUDED.updated_at IS NOT NULL
                AND (u.updated_at IS NULL OR EXCLUDED.updated_at > u.updated_at)
        $q$;
    END IF;

    SELECT COALESCE(max(id), 0) INTO next_id FROM "user".user_devices;
    IF to_regclass('public.user_devices') IS NOT NULL THEN
        EXECUTE 'SELECT COALESCE(max(id), 0) FROM public.user_devices' INTO max_id;
        next_id := GREATEST(next_id, max_id);
        FOR r IN EXECUTE 'SELECT id, user_id, device_id, platform, push_token, ip, location, last_active_at, created_at FROM public.user_devices ORDER BY id' LOOP
            SELECT id, last_active_at INTO existing_id, existing_at
              FROM "user".user_devices
             WHERE user_id IS NOT DISTINCT FROM r.user_id
               AND device_id IS NOT DISTINCT FROM r.device_id;
            IF FOUND THEN
                IF r.last_active_at IS NOT NULL AND (existing_at IS NULL OR r.last_active_at > existing_at) THEN
                    UPDATE "user".user_devices
                       SET platform = r.platform, push_token = r.push_token,
                           ip = r.ip, location = r.location,
                           last_active_at = r.last_active_at, created_at = r.created_at
                     WHERE id = existing_id;
                END IF;
            ELSE
                IF EXISTS (SELECT 1 FROM "user".user_devices WHERE id = r.id) THEN
                    next_id := next_id + 1;
                    existing_id := next_id;
                ELSE
                    existing_id := r.id;
                END IF;
                INSERT INTO "user".user_devices (id, user_id, device_id, platform, push_token, ip, location, last_active_at, created_at)
                VALUES (existing_id, r.user_id, r.device_id, r.platform, r.push_token, r.ip, r.location, r.last_active_at, r.created_at);
            END IF;
        END LOOP;
    END IF;

    -- Remap a colliding group id before copying friendships that refer to it.
    -- Keep each distinct group/request; exact duplicate copies need no new id.
    CREATE TEMP TABLE p3_group_ids (old_id BIGINT PRIMARY KEY, new_id BIGINT NOT NULL) ON COMMIT DROP;
    FOREACH source_schema IN ARRAY ARRAY['friend', 'public'] LOOP
        TRUNCATE p3_group_ids;
        IF to_regclass(format('%I.friend_groups', source_schema)) IS NOT NULL THEN
            EXECUTE format('SELECT COALESCE(max(id), 0) FROM %I.friend_groups', source_schema) INTO max_id;
            SELECT GREATEST(COALESCE(max(id), 0), max_id) INTO next_id FROM "user".friend_groups;
            FOR r IN EXECUTE format('SELECT id, user_id, name, sort_order, created_at FROM %I.friend_groups ORDER BY id', source_schema) LOOP
                SELECT id INTO existing_id FROM "user".friend_groups
                 WHERE id = r.id AND user_id = r.user_id AND name = r.name
                   AND sort_order = r.sort_order AND created_at IS NOT DISTINCT FROM r.created_at;
                IF NOT FOUND THEN
                    IF EXISTS (SELECT 1 FROM "user".friend_groups WHERE id = r.id) THEN
                        next_id := next_id + 1;
                        existing_id := next_id;
                    ELSE
                        existing_id := r.id;
                    END IF;
                    INSERT INTO "user".friend_groups (id, user_id, name, sort_order, created_at)
                    VALUES (existing_id, r.user_id, r.name, r.sort_order, r.created_at);
                END IF;
                INSERT INTO p3_group_ids VALUES (r.id, existing_id);
            END LOOP;
        END IF;

        IF to_regclass(format('%I.friends', source_schema)) IS NOT NULL THEN
            EXECUTE format('SELECT COALESCE(max(id), 0) FROM %I.friends', source_schema) INTO max_id;
            SELECT GREATEST(COALESCE(max(id), 0), max_id) INTO next_id FROM "user".friends;
            FOR r IN EXECUTE format('SELECT id, user_id, friend_id, group_id, remark, created_at FROM %I.friends ORDER BY id', source_schema) LOOP
                SELECT COALESCE((SELECT new_id FROM p3_group_ids WHERE old_id = r.group_id), r.group_id) INTO mapped_group_id;
                SELECT id, created_at INTO existing_id, existing_at FROM "user".friends WHERE user_id = r.user_id AND friend_id = r.friend_id;
                IF FOUND THEN
                    -- The newer relationship wins duplicate pairs; ties keep user.
                    IF r.created_at IS NOT NULL AND (existing_at IS NULL OR r.created_at > existing_at) THEN
                        UPDATE "user".friends SET group_id = mapped_group_id, remark = r.remark, created_at = r.created_at WHERE id = existing_id;
                    END IF;
                ELSE
                    IF EXISTS (SELECT 1 FROM "user".friends WHERE id = r.id) THEN
                        next_id := next_id + 1;
                        existing_id := next_id;
                    ELSE
                        existing_id := r.id;
                    END IF;
                    INSERT INTO "user".friends (id, user_id, friend_id, group_id, remark, created_at)
                    VALUES (existing_id, r.user_id, r.friend_id, mapped_group_id, r.remark, r.created_at);
                END IF;
            END LOOP;
        END IF;

        IF to_regclass(format('%I.friend_requests', source_schema)) IS NOT NULL THEN
            EXECUTE format('SELECT COALESCE(max(id), 0) FROM %I.friend_requests', source_schema) INTO max_id;
            SELECT GREATEST(COALESCE(max(id), 0), max_id) INTO next_id FROM "user".friend_requests;
            FOR r IN EXECUTE format('SELECT id, from_user_id, to_user_id, message, status, created_at, updated_at FROM %I.friend_requests ORDER BY id', source_schema) LOOP
                SELECT id INTO existing_id FROM "user".friend_requests
                 WHERE id = r.id AND from_user_id = r.from_user_id AND to_user_id = r.to_user_id
                   AND message = r.message AND status = r.status
                   AND created_at IS NOT DISTINCT FROM r.created_at AND updated_at IS NOT DISTINCT FROM r.updated_at;
                IF NOT FOUND THEN
                    IF EXISTS (SELECT 1 FROM "user".friend_requests WHERE id = r.id) THEN
                        next_id := next_id + 1;
                        existing_id := next_id;
                    ELSE
                        existing_id := r.id;
                    END IF;
                    INSERT INTO "user".friend_requests (id, from_user_id, to_user_id, message, status, created_at, updated_at)
                    VALUES (existing_id, r.from_user_id, r.to_user_id, r.message, r.status, r.created_at, r.updated_at);
                END IF;
            END LOOP;
        END IF;

        IF to_regclass(format('%I.user_blocks', source_schema)) IS NOT NULL THEN
            EXECUTE format('SELECT COALESCE(max(id), 0) FROM %I.user_blocks', source_schema) INTO max_id;
            SELECT GREATEST(COALESCE(max(id), 0), max_id) INTO next_id FROM "user".user_blocks;
            FOR r IN EXECUTE format('SELECT id, user_id, blocked_user_id, created_at FROM %I.user_blocks ORDER BY id', source_schema) LOOP
                -- Pair is the identity, not the unrelated legacy surrogate id.
                SELECT id INTO existing_id FROM "user".user_blocks WHERE user_id = r.user_id AND blocked_user_id = r.blocked_user_id;
                IF NOT FOUND THEN
                    IF EXISTS (SELECT 1 FROM "user".user_blocks WHERE id = r.id) THEN
                        next_id := next_id + 1;
                        existing_id := next_id;
                    ELSE
                        existing_id := r.id;
                    END IF;
                    INSERT INTO "user".user_blocks (id, user_id, blocked_user_id, created_at)
                    VALUES (existing_id, r.user_id, r.blocked_user_id, COALESCE(r.created_at, NOW()));
                END IF;
            END LOOP;
        END IF;
    END LOOP;
    DROP TABLE p3_group_ids;

    -- Re-create serial defaults in the destination before removing their source
    -- sequences. ALTER SEQUENCE RESTART is transactional (unlike setval), and
    -- never moves an existing sequence backwards on repeated execution.
    FOREACH t IN ARRAY ARRAY['users', 'user_devices', 'friends', 'friend_groups', 'friend_requests', 'user_blocks'] LOOP
        needs_sequence := t IN ('user_devices', 'friend_groups', 'user_blocks');
        sequence_value := 1;
        FOREACH source_schema IN ARRAY ARRAY['user', 'friend', 'public'] LOOP
            IF to_regclass(format('%I.%I', source_schema, t)) IS NOT NULL THEN
                sequence_name := pg_get_serial_sequence(format('%I.%I', source_schema, t), 'id');
                IF sequence_name IS NOT NULL THEN
                    needs_sequence := true;
                    EXECUTE format('SELECT last_value, is_called FROM %s', sequence_name) INTO max_id, sequence_called;
                    sequence_value := GREATEST(sequence_value, max_id + CASE WHEN sequence_called THEN 1 ELSE 0 END);
                END IF;
            END IF;
        END LOOP;
        IF needs_sequence THEN
            sequence_name := format('"user".%I', t || '_id_seq');
            EXECUTE format('CREATE SEQUENCE IF NOT EXISTS %s', sequence_name);
            EXECUTE format('SELECT last_value, is_called FROM %s', sequence_name) INTO max_id, sequence_called;
            sequence_value := GREATEST(sequence_value, max_id + CASE WHEN sequence_called THEN 1 ELSE 0 END);
            EXECUTE format('SELECT COALESCE(max(id), 0) + 1 FROM "user".%I', t) INTO max_id;
            EXECUTE format('ALTER SEQUENCE %s RESTART WITH %s', sequence_name, GREATEST(max_id, sequence_value));
            EXECUTE format('ALTER SEQUENCE %s OWNED BY "user".%I.id', sequence_name, t);
            EXECUTE format('ALTER TABLE "user".%I ALTER COLUMN id SET DEFAULT nextval(%L::regclass)', t, sequence_name);
        END IF;
    END LOOP;

    CREATE UNIQUE INDEX IF NOT EXISTS idx_users_username ON "user".users(username);
    CREATE UNIQUE INDEX IF NOT EXISTS idx_users_phone ON "user".users(phone) WHERE phone <> '';
    CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email ON "user".users(email) WHERE email <> '';
    CREATE UNIQUE INDEX IF NOT EXISTS idx_user_devices_unique ON "user".user_devices(user_id, device_id);
    CREATE INDEX IF NOT EXISTS idx_user_devices_user_id ON "user".user_devices(user_id);
    CREATE UNIQUE INDEX IF NOT EXISTS idx_friends_pair ON "user".friends(user_id, friend_id);
    CREATE INDEX IF NOT EXISTS idx_friends_user ON "user".friends(user_id);
    CREATE INDEX IF NOT EXISTS idx_friends_friend ON "user".friends(friend_id);
    CREATE INDEX IF NOT EXISTS idx_friend_groups_user ON "user".friend_groups(user_id);
    CREATE INDEX IF NOT EXISTS idx_friend_requests_from ON "user".friend_requests(from_user_id);
    CREATE INDEX IF NOT EXISTS idx_friend_requests_to ON "user".friend_requests(to_user_id);
    CREATE INDEX IF NOT EXISTS idx_friend_requests_status ON "user".friend_requests(status);
    CREATE UNIQUE INDEX IF NOT EXISTS idx_user_blocks_pair ON "user".user_blocks(user_id, blocked_user_id);
    CREATE INDEX IF NOT EXISTS idx_user_blocks_user ON "user".user_blocks(user_id);

    DROP TABLE IF EXISTS public.friends, public.friend_groups, public.friend_requests, public.user_blocks;
    DROP TABLE IF EXISTS public.users, public.user_devices;
    DROP TABLE IF EXISTS friend.friends, friend.friend_groups, friend.friend_requests, friend.user_blocks;
    DROP SEQUENCE IF EXISTS friend.friends_id_seq, friend.friend_groups_id_seq,
        friend.friend_requests_id_seq, friend.user_blocks_id_seq;
    DROP SCHEMA IF EXISTS friend;
END
$$;
