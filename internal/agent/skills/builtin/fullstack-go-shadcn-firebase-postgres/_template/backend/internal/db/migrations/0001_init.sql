CREATE TABLE users (
    id           bigserial PRIMARY KEY,
    firebase_uid text NOT NULL UNIQUE,
    email        text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE items (
    id         bigserial PRIMARY KEY,
    owner_id   bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    title      text NOT NULL,
    done       boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX items_owner_idx ON items (owner_id, created_at DESC);
