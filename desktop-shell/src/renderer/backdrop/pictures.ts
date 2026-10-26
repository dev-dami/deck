// The pictures the backdrop prints: Deck's public-domain library (art/,
// credits in art/ATTRIBUTION.md and art/met/manifest.json) and the
// person's own wallpaper. Vite bundles each file, so it loads under
// img-src 'self'.

export interface Picture {
  id: string;
  url: string;
  // The quiet line under the picture: who made it, what, and when.
  credit: string;
  // Where it came from, and its licence.
  source: string;
  // The paper margin or mount to cut away before printing, as fractions of
  // the file: top, right, bottom, left.
  crop?: [number, number, number, number];
  // Where the subject sits, so a crop to the window keeps it (0..1, as
  // `place` takes).
  ax: number;
  ay: number;
  // A subject photographed on a plain ground (Blossfeldt's plants): it is
  // set whole on the right, not cropped to fill, and its dark tones are
  // the ink on any stock.
  figure?: boolean;
}

const GETTY = "J. Paul Getty Museum, CC0";
const MET = "The Metropolitan Museum of Art, CC0";

// Each URL is a literal, so Vite finds and bundles the file.
export const PICTURES: readonly Picture[] = [
  // Liquid Glass: scenes with a sky, at dusk in its inks.
  {
    id: "alps",
    url: new URL("../art/alpine-braun-1870.jpg", import.meta.url).href,
    credit: "Adolphe Braun, French Alps, about 1870",
    source: GETTY,
    ax: 0.5,
    ay: 0.35,
  },
  {
    id: "yosemite",
    url: new URL("../art/met/watkins-looking-down-yosemite-valley-264908.jpg", import.meta.url).href,
    credit: "Carleton E. Watkins, Looking Down Yosemite Valley, about 1872",
    source: MET,
    crop: [0.07, 0.1, 0.17, 0.11],
    ax: 0.5,
    ay: 0.45,
  },
  {
    id: "hira-snow",
    url: new URL("../art/met/hiroshige-evening-snow-on-hira-lake-biwa-36533.jpg", import.meta.url).href,
    credit: "Utagawa Hiroshige, Evening Snow on Hira, Lake Biwa, about 1835",
    source: MET,
    crop: [0.06, 0.03, 0.07, 0.03],
    ax: 0.6,
    ay: 0.5,
  },
  {
    id: "clouds",
    url: new URL("../art/met/constable-cloud-study-727706.jpg", import.meta.url).href,
    credit: "John Constable, Cloud study, 1830–35",
    source: MET,
    crop: [0.1, 0.04, 0.08, 0.04],
    ax: 0.5,
    ay: 0.6,
  },
  {
    id: "sete",
    url: new URL("../art/met/le-gray-mediterranean-sea-sete-283124.jpg", import.meta.url).href,
    credit: "Gustave Le Gray, Mediterranean Sea, Sète, 1857",
    source: MET,
    crop: [0.02, 0.02, 0.02, 0.02],
    ax: 0.5,
    ay: 0.5,
  },
  // Riso: strong forms that a screen and one ink can carry.
  {
    id: "allium",
    url: new URL("../art/allium-blossfeldt-1928.jpg", import.meta.url).href,
    credit: "Karl Blossfeldt, Allium oreophilum, 1928",
    source: GETTY,
    ax: 0.5,
    ay: 0.5,
    figure: true,
  },
  {
    id: "great-wave",
    url: new URL("../art/met/hokusai-under-the-wave-off-kanagawa-also-known-a-36491.jpg", import.meta.url).href,
    credit: "Katsushika Hokusai, Under the Wave off Kanagawa, about 1830–32",
    source: MET,
    crop: [0.03, 0.025, 0.03, 0.025],
    ax: 0.3,
    ay: 0.5,
  },
  {
    id: "irises",
    url: new URL("../art/met/korin-irises-at-yatsuhashi-39664.jpg", import.meta.url).href,
    credit: "Ogata Kōrin, Irises at Yatsuhashi, after 1709",
    source: MET,
    crop: [0.035, 0.025, 0.04, 0.025],
    ax: 0.75,
    ay: 0.6,
  },
  {
    id: "plum-moon",
    url: new URL("../art/met/jakuchu-white-plum-blossoms-and-moon-53408.jpg", import.meta.url).href,
    credit: "Itō Jakuchū, White Plum Blossoms and Moon, 1755",
    source: MET,
    crop: [0.06, 0.13, 0.05, 0.13],
    ax: 0.5,
    ay: 0.3,
  },
  // Stitch: bold prints that chart well in a few threads.
  {
    id: "nagasaki",
    url: new URL("../art/nagasaki-boats-beato-1865.jpg", import.meta.url).href,
    credit: "Felice Beato, boats at Nagasaki, about 1865",
    source: GETTY,
    ax: 0.5,
    ay: 0.55,
  },
  {
    id: "red-fuji",
    url: new URL("../art/met/hokusai-south-wind-clear-sky-also-known-as-red-f-36490.jpg", import.meta.url).href,
    credit: "Katsushika Hokusai, South Wind, Clear Sky (Red Fuji), about 1830–32",
    source: MET,
    crop: [0.025, 0.02, 0.025, 0.02],
    ax: 0.6,
    ay: 0.5,
  },
  {
    id: "shower-bridge",
    url: new URL("../art/met/hiroshige-sudden-shower-over-shin-ohashi-bridge-an-36461.jpg", import.meta.url).href,
    credit: "Utagawa Hiroshige, Sudden Shower over Shin-Ōhashi Bridge and Atake, 1857",
    source: MET,
    crop: [0.12, 0.03, 0.06, 0.03],
    ax: 0.5,
    ay: 0.6,
  },
  {
    id: "kintai",
    url: new URL("../art/met/hokusai-kintai-bridge-in-suo-province-36489.jpg", import.meta.url).href,
    credit: "Katsushika Hokusai, Kintai Bridge in Suō Province, 1827–30",
    source: MET,
    crop: [0.03, 0.025, 0.03, 0.025],
    ax: 0.5,
    ay: 0.5,
  },
  {
    id: "canyon",
    url: new URL("../art/black-canyon-osullivan-1871.jpg", import.meta.url).href,
    credit: "Timothy H. O'Sullivan, Black Cañon, Colorado River, 1871",
    source: GETTY,
    ax: 0.5,
    ay: 0.45,
  },
  // Tokyo Night: deep space in its own colours.
  {
    id: "xdf",
    url: new URL("../art/hubble-xdf-nasa-2012.jpg", import.meta.url).href,
    credit: "Hubble eXtreme Deep Field, NASA, 2012",
    source: "NASA, public domain",
    ax: 0.5,
    ay: 0.5,
  },
];

// Each art direction's own set, its default first. The picker offers the
// set of the theme on screen; any picture prints through any process.
export const SETS: Record<"glass" | "riso" | "stitch", readonly string[]> = {
  glass: ["alps", "yosemite", "hira-snow", "clouds", "sete", "xdf"],
  riso: ["allium", "great-wave", "irises", "plum-moon"],
  stitch: ["canyon", "nagasaki", "red-fuji", "shower-bridge", "kintai"],
};

// The person's own picture, from IndexedDB (wallpaper.ts).
export const CUSTOM = "custom";

export function pictureInfo(id: string): Picture | undefined {
  return PICTURES.find((p) => p.id === id);
}

// How a picture is framed for the processes (canvas.ts Framing). The
// person's own picture is centred and kept whole to its edges.
export function framingOf(p: Picture | undefined): { ax: number; ay: number; figure: boolean; crop: [number, number, number, number] } {
  return { ax: p?.ax ?? 0.5, ay: p?.ay ?? 0.5, figure: p?.figure === true, crop: p?.crop ?? [0, 0, 0, 0] };
}
