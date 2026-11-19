-- Own accounts: what govue's account.Store needs, beside the owner row.
--
-- The owner row stays the identity. owners.id is the account id the session and
-- the owner API use. This table only holds the way in: a login, a password hash
-- and a TOTP secret. A later second way in (OIDC, passkeys) adds its own table
-- that points at the same owners row, never a second identity.
--
-- Nothing here touches lists, wishes or the guest side.

create table owner_credentials (
    owner_id bigint primary key references owners (id) on delete cascade,

    -- The login as the person typed it, shown back to them.
    login text not null,
    -- The login folded to lower case by the service. It is the unique key, so
    -- two spellings of one address cannot be two accounts.
    login_key text not null,

    -- Argon2id in the PHC string format. Never the password.
    password_hash text not null,

    -- The base32 TOTP secret, stored in the clear: the account contract allows
    -- encryption at rest and does not require it. The database already holds
    -- everything an attacker with read access needs for the password hash.
    totp_secret text not null,
    -- RFC 6238 counter of the last code used. 0 means the secret is not
    -- confirmed yet. It only moves forward, which refuses a replayed code.
    totp_counter bigint not null default 0,

    -- A disabled account cannot be found for login. Nothing sets it yet: it is
    -- the switch a later operator tool or account deletion uses.
    disabled   boolean     not null default false,
    created_at timestamptz not null default now(),

    constraint owner_credentials_login_not_empty check (length(login) > 0 and length(login_key) > 0),
    constraint owner_credentials_counter_not_negative check (totp_counter >= 0)
);

create unique index owner_credentials_login_key_idx on owner_credentials (login_key);
