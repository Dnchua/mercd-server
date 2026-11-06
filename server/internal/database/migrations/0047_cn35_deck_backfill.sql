-- Preserve player-created first-party gaps after moving units to the waiting area.
ALTER TABLE cn35_player_decks ADD COLUMN starter_backfill_disabled INTEGER NOT NULL DEFAULT 0 CHECK (starter_backfill_disabled IN (0, 1));
