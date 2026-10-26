# Deck's site

The page at the front of the project: what Deck is, and the way to the
releases and the source. Next.js (App Router), one static page.

```sh
npm install
npm run dev        # http://localhost:4196
npm run build      # production build
npm run start      # serves the build on port 4196
npm run lint && npm run typecheck
```

## Pictures

`public/art/` holds four works from The Metropolitan Museum of Art's Open
Access program, all public domain (CC0): Utagawa Hiroshige, *Sudden Shower
over Shin-Ōhashi Bridge and Atake* (1857); Carleton E. Watkins, *Yosemite
Valley from Union Point* (about 1872); Itō Jakuchū, *White Plum Blossoms
and Moon* (1755); Ogata Kōrin, *Irises at Yatsuhashi* (after 1709). They
are cropped to the picture and saved as WebP. The page credits each one.

`public/shots/` holds screenshots of the desktop and terminal apps.

The fonts in `app/fonts/` (Instrument Serif, Instrument Sans, JetBrains
Mono) are under the SIL Open Font License; their licences sit beside them.
