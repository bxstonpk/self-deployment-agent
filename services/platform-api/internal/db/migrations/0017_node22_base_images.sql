-- Moves the existing Node-built runtimes off node:20-alpine. Node 20 left
-- upstream support in April 2026, and the first real deploy of the LAN
-- starter templates found the image's bundled npm carrying a CRITICAL
-- (CVE-2026-59873 in npm's own `tar`). Runtime stages no longer ship npm at
-- all (see buildengine/templates.go); this keeps the build images inside
-- their support window too, which is what FR-022's ongoing governance of
-- base images asks for. For react and vue this is only the image their
-- build runs on — they run on nginx.
UPDATE base_images SET image_reference = 'node:22-alpine'
WHERE runtime IN ('nodejs', 'react', 'vue', 'nextjs') AND image_reference = 'node:20-alpine';
