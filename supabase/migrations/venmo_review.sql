-- Venmo review: manual categorization before counting toward expenses/budget.

ALTER TABLE transactions
  ADD COLUMN IF NOT EXISTS review_status TEXT NOT NULL DEFAULT 'none'
    CHECK (review_status IN ('none', 'pending', 'categorized')),
  ADD COLUMN IF NOT EXISTS user_note TEXT;

-- Existing Venmo-tagged transactions need review.
UPDATE transactions t
SET review_status = 'pending'
FROM categories c
WHERE t.category_id = c.id
  AND c.name = 'Venmo'
  AND t.review_status = 'none';
