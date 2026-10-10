// The word "djinn." beside the lamp: its letters are DM Sans 600 (OFL, as the interface's type), the dots of
// the j and the i are two puffs of smoke rising, and its period is brass, as the lamp. The letters take the text's
// colour, so the word follows the theme. Drawn once from the font's outlines (fontTools, by hand): no font loads
// for it, and it reads the same everywhere.

const letters = [
  "M291 1004Q221 1004 166.0 970.0Q111 936 80.5 876.0Q50 816 50.0 740.0Q50 664 81.0 604.5Q112 545 167.5 510.5Q223 476 293 476Q350 476 392.5 497.5Q435 519 460 558V272H580V992H472L460 919Q444 941 421.0 960.5Q398 980 366.0 992.0Q334 1004 291 1004ZM316 900Q359 900 392.0 879.5Q425 859 443.0 823.0Q461 787 461.0 740.0Q461 693 443.0 657.0Q425 621 392.0 601.0Q359 581 316 581Q275 581 242.0 601.0Q209 621 190.5 657.0Q172 693 172.0 740.0Q172 787 190.5 823.0Q209 859 242.0 879.5Q275 900 316 900Z",
  "M577.0 1212V1110H618.0Q652.0 1110 666.0 1096.5Q680.0 1083 680.0 1050V488H800.0V1051Q800.0 1109 780.0 1144.5Q760.0 1180 723.5 1196.0Q687.0 1212 636.0 1212Z",
  "M881.0 992V488H1001.0V992Z",
  "M1095.0 992V488H1201.0L1210.0 572Q1233.0 528 1276.0 502.0Q1319.0 476 1378.0 476Q1439.0 476 1482.0 501.5Q1525.0 527 1548.5 576.0Q1572.0 625 1572.0 698V992H1452.0V709Q1452.0 646 1424.0 612.0Q1396.0 578 1341.0 578Q1305.0 578 1276.5 595.0Q1248.0 612 1231.5 644.5Q1215.0 677 1215.0 723V992Z",
  "M1659.0 992V488H1765.0L1774.0 572Q1797.0 528 1840.0 502.0Q1883.0 476 1942.0 476Q2003.0 476 2046.0 501.5Q2089.0 527 2112.5 576.0Q2136.0 625 2136.0 698V992H2016.0V709Q2016.0 646 1988.0 612.0Q1960.0 578 1905.0 578Q1869.0 578 1840.5 595.0Q1812.0 612 1795.5 644.5Q1779.0 677 1779.0 723V992Z",
];

export function Wordmark() {
  return (
    <svg
      className="wordmark"
      viewBox="0 140 2366 1162"
      role="img"
      aria-label="djinn"
    >
      {letters.map((d, i) => (
        <path key={i} d={d} fill="currentColor" />
      ))}
      <circle className="wordmark-puff" cx="715" cy="318" r="62" />
      <circle className="wordmark-puff high" cx="971" cy="218" r="78" />
      <circle className="wordmark-period" cx="2291" cy="917" r="75" />
    </svg>
  );
}
