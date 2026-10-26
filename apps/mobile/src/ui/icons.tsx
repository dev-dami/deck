import Svg, { Circle, Path, Rect } from "react-native-svg";

export type IconName =
  | "back"
  | "close"
  | "chevron-right"
  | "chevron-down"
  | "check"
  | "plus"
  | "send"
  | "clock"
  | "sliders"
  | "laptop"
  | "folder"
  | "file"
  | "key"
  | "copy"
  | "refresh"
  | "offline"
  | "terminal"
  | "shield"
  | "check-circle";

const strokes: Record<IconName, string[]> = {
  back: ["m15 6-6 6 6 6"],
  close: ["M18 6 6 18", "M6 6l12 12"],
  "chevron-right": ["m9 6 6 6-6 6"],
  "chevron-down": ["m6 9 6 6 6-6"],
  check: ["M20 6 9 17l-5-5"],
  plus: ["M12 5v14", "M5 12h14"],
  send: ["M12 19V5", "M5 12l7-7 7 7"],
  clock: ["M12 7v5l3 2"],
  sliders: ["M4 7h9", "M17 7h3", "M4 17h3", "M11 17h9"],
  laptop: ["M2 20h20"],
  folder: ["M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2Z"],
  file: ["M14 3H6a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z", "M14 3v6h6"],
  key: ["m10.8 12.2 9.2-9.2", "M16 7l3 3"],
  copy: ["M5 15V6a2 2 0 0 1 2-2h9"],
  refresh: ["M20 11a8 8 0 1 0-2.3 5.7", "M20 4v7h-7"],
  offline: [
    "M2 2l20 20",
    "M8.5 16.5a5 5 0 0 1 7 0",
    "M5 13a10 10 0 0 1 5.2-2.8",
    "M19 13a10 10 0 0 0-2.3-1.7",
  ],
  terminal: ["m4 17 6-6-6-6", "M12 19h8"],
  shield: ["M12 3 5 6v5c0 5 3 8 7 10 4-2 7-5 7-10V6z", "M12 9v4", "M12 16h.01"],
  "check-circle": ["M8 12l3 3 5-6"],
};

export function Icon({
  name,
  color,
  size = 20,
  weight = 2,
}: {
  name: IconName;
  color: string;
  size?: number;
  weight?: number;
}) {
  return (
    <Svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke={color}
      strokeWidth={weight}
      strokeLinecap="round"
      strokeLinejoin="round"
      accessible={false}
    >
      {(name === "clock" || name === "check-circle") && <Circle cx={12} cy={12} r={9} />}
      {name === "sliders" && (
        <>
          <Circle cx={15} cy={7} r={2} />
          <Circle cx={9} cy={17} r={2} />
        </>
      )}
      {name === "laptop" && <Rect x={3} y={4} width={18} height={12} rx={2} />}
      {name === "key" && <Circle cx={8} cy={15} r={4} />}
      {name === "copy" && <Rect x={9} y={9} width={11} height={11} rx={2} />}
      {strokes[name].map((d) => (
        <Path key={d} d={d} />
      ))}
    </Svg>
  );
}
