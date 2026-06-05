-- Drop pseudo_user_id column from bots table
-- This column is no longer needed since bot.id is used directly
ALTER TABLE bot.bots DROP COLUMN IF EXISTS pseudo_user_id;
