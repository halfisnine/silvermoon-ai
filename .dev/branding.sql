-- Site customization for this dedicated PostgreSQL development environment.
-- Preserve upstream code/attribution and any branding already customized by the operator.
INSERT INTO options (key, value)
VALUES ('SystemName', '银月'), ('Logo', '/silvermoon/moonlight.webp')
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value
WHERE (options.key = 'SystemName' AND options.value IN ('', 'New API'))
   OR (options.key = 'Logo' AND options.value IN ('', '/logo.png'))
RETURNING key;
