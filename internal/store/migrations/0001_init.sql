-- The whole schema of the wishlist service.
--
-- Two halves that never meet in a query: owners, lists and wishes, which the
-- owner of a list reads and writes, and reservations, which only the guest side
-- reads. The owner never learns what was taken (docs/adr/0001-blind-reservations.md),
-- and that is a property of the queries: only guest.go in internal/store names
-- the reservations table, and boundary_test.go fails when another file does.

create table owners (
    id           bigint generated always as identity primary key,
    -- The OIDC subject, stable across a rename at the identity provider.
    subject      text        not null unique,
    username     text        not null,
    display_name text        not null default '',
    email        text        not null default '',
    created_at   timestamptz not null default now(),
    last_seen_at timestamptz not null default now()
);

create table lists (
    id       bigint generated always as identity primary key,
    owner_id bigint not null references owners (id) on delete cascade,

    title      text not null,
    occasion   text not null default '',
    event_date date,
    greeting   text not null default '',

    -- The guest link. null turns it off without losing the reservations behind
    -- it; a rotation is a new value, which invalidates every link handed out.
    share_token text unique,

    -- Off closes the list for guests: they still see it, they cannot take
    -- anything any more.
    reserve_open boolean     not null default true,
    archived     boolean     not null default false,
    created_at   timestamptz not null default now(),
    updated_at   timestamptz not null default now(),

    constraint lists_title_not_empty check (length(btrim(title)) > 0)
);

create index lists_owner_idx on lists (owner_id, archived, created_at desc);

create table wishes (
    id      bigint generated always as identity primary key,
    list_id bigint not null references lists (id) on delete cascade,

    title       text not null,
    description text not null default '',
    url         text not null default '',
    shop        text not null default '',

    -- null is a wish without a price, which the guest side shows as unknown
    -- rather than as free.
    price_cents bigint,
    currency    text not null default 'EUR',

    -- Either the object key of an uploaded picture in the object store, or a
    -- remote URL taken from the shop page. Never both: uploading replaces the
    -- remote one.
    image_key text not null default '',
    image_url text not null default '',

    -- 0 an idea, 1 normal, 2 the most wanted.
    priority   smallint not null default 1,
    quantity   integer  not null default 1,
    group_gift boolean  not null default false,

    -- Manual order inside the list, ascending, ties broken by id.
    position   integer     not null default 0,
    archived   boolean     not null default false,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),

    constraint wishes_title_not_empty check (length(btrim(title)) > 0),
    constraint wishes_priority_range check (priority between 0 and 2),
    constraint wishes_quantity_positive check (quantity >= 1),
    constraint wishes_price_not_negative check (price_cents is null or price_cents >= 0),
    constraint wishes_currency_code check (currency ~ '^[A-Z]{3}$')
);

create index wishes_list_idx on wishes (list_id, archived, position, id);

-- The guest side only. guest_key is the value of the guest's own cookie: it is
-- what lets somebody undo their own entry without an account, and nothing else.
create table reservations (
    id      bigint generated always as identity primary key,
    wish_id bigint not null references wishes (id) on delete cascade,

    guest_key  text    not null,
    guest_name text    not null default '',
    quantity   integer not null default 1,

    -- reserved: taken care of. bought: already in the cupboard. Both hide the
    -- wish from the other guests the same way.
    state text not null default 'bought',

    -- A pledge towards a group gift, null for a plain reservation.
    amount_cents bigint,

    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),

    constraint reservations_state check (state in ('reserved', 'bought')),
    constraint reservations_quantity_positive check (quantity >= 1),
    constraint reservations_amount_positive check (amount_cents is null or amount_cents > 0)
);

create index reservations_wish_idx on reservations (wish_id);

-- One entry per guest and wish: a second click edits the first rather than
-- adding to it, so a reload cannot take a wish twice. It is not a cap on a
-- person: somebody with the link can clear their cookie and come back as a new
-- guest, which is inherent to guests without accounts.
create unique index reservations_guest_idx on reservations (wish_id, guest_key);

-- Questions about a wish. visibility guests keeps a thread among the guests,
-- visibility owner is a question the guest deliberately asked the owner, who
-- then sees the text but still nothing about reservations. The guest side reads
-- both kinds, so an owner's answer (always visibility owner) reaches the guests
-- as well.
create table comments (
    id      bigint generated always as identity primary key,
    wish_id bigint not null references wishes (id) on delete cascade,

    author_kind text not null,
    -- Empty for the owner's own replies: the UI names them.
    guest_key   text not null default '',
    author      text not null default '',
    body        text not null,
    visibility  text not null default 'guests',

    created_at timestamptz not null default now(),

    constraint comments_author_kind check (author_kind in ('guest', 'owner')),
    constraint comments_visibility check (visibility in ('guests', 'owner')),
    constraint comments_body_not_empty check (length(btrim(body)) > 0),
    -- The owner's own reply is the only comment the owner may write, and it is
    -- always readable by the guests it answers.
    constraint comments_owner_replies_visibly check (author_kind = 'guest' or visibility = 'owner')
);

create index comments_wish_idx on comments (wish_id, created_at);
