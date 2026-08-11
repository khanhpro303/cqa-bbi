# BBI web preview assets

This folder keeps the browser, iOS/PWA, Open Graph, and Twitter Card assets in one place.

- `logo/bbi-logo.png`: PNG copy of the original `frontend/public/bbi-logo.ico` mark.
- `social/bbi-social-preview.png`: shared 1200x630 Open Graph and Twitter large-card preview.
- `icons/apple-touch-icon.png`: 180x180 iOS Home Screen icon.
- `icons/icon-192.png` and `icons/icon-512.png`: web app manifest icons.
- `icons/favicon.ico`, `icons/favicon-16.png`, and `icons/favicon-32.png`: browser favicons.
- `site.webmanifest`: installable web app metadata.

The two abstract backgrounds were generated with the built-in image generation workflow using the original BBI logo as the palette reference. The original logo and all text were composited locally so the mark and Vietnamese copy remain exact.

Generation prompt set:

- Social preview: premium abstract SaaS share-card background using the logo's coral-pink, warm-orange, and near-black palette; translucent rounded panels and connected data lights; quiet center-left copy area; no text or redrawn logo.
- App icons: minimal square app-icon background using the same palette; near-black quiet center, warm glowing perimeter, safe central logo area; no text or redrawn logo.

Public references use a `?v=1` cache key. Increment it in `frontend/index.html` and `site.webmanifest` whenever a branded bitmap is replaced.
