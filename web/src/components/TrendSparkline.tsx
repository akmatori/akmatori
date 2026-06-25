interface TrendSparklineProps {
  buckets: number[];
}

const SVG_WIDTH = 88;
const SVG_HEIGHT = 24;
const BAR_GAP = 1;

export default function TrendSparkline({ buckets }: TrendSparklineProps) {
  const total = buckets.reduce((s, v) => s + v, 0);
  const isEmpty = buckets.length === 0 || total === 0;

  if (isEmpty) {
    return (
      <span
        aria-label="No alerts in window"
        className="inline-block align-middle opacity-30"
        style={{ width: SVG_WIDTH, height: SVG_HEIGHT }}
      >
        <svg width={SVG_WIDTH} height={SVG_HEIGHT} aria-hidden="true">
          <line
            x1={0}
            y1={SVG_HEIGHT / 2}
            x2={SVG_WIDTH}
            y2={SVG_HEIGHT / 2}
            stroke="currentColor"
            strokeWidth={1}
          />
        </svg>
      </span>
    );
  }

  const maxVal = Math.max(...buckets, 1);
  const n = buckets.length;
  const barWidth = (SVG_WIDTH - BAR_GAP * (n - 1)) / n;

  return (
    <svg
      width={SVG_WIDTH}
      height={SVG_HEIGHT}
      aria-label={`${total} alert${total === 1 ? '' : 's'} in window`}
      className="inline-block align-middle"
    >
      <title>{`${total} alert${total === 1 ? '' : 's'} in window`}</title>
      {buckets.map((val, i) => {
        const barH = Math.max(2, (val / maxVal) * SVG_HEIGHT);
        const x = i * (barWidth + BAR_GAP);
        const y = SVG_HEIGHT - barH;
        return (
          <rect
            key={i}
            x={x}
            y={y}
            width={barWidth}
            height={barH}
            fill="currentColor"
            opacity={0.5}
            rx={1}
          />
        );
      })}
    </svg>
  );
}
