// The reports of a wish, in a section of their own on its screen, right after what waits for you: what a worker
// found out, for the developer to read before deciding. Each shows its title, who wrote it and when, and the first
// view of its Markdown, which "Open the report" unfolds in place. A question that names a report links it too
// (src/data/reports.ts).
import { FileText } from "lucide-react";
import { memo } from "react";

import { type Block } from "../gen/ts/plan/v1/plan_pb";
import { when } from "./data/format";
import { t } from "./i18n";
import { FoldedMarkdown } from "./markdown-body";
import { SafeSvg } from "./safe-svg";

export const WishReports = memo(function WishReports({
  reports,
  codes,
}: {
  // The latest first.
  reports: readonly Block[];
  // The code of each task, by its id: the worker that wrote a report.
  codes: ReadonlyMap<string, string>;
}) {
  return (
    <section
      className="wish-section wish-reports"
      id="reports"
      aria-label={t("reports.title")}
    >
      <div className="section-title">
        <h2>
          {t("reports.title")}
          <span className="count">{reports.length}</span>
        </h2>
        <p>{t("reports.detail")}</p>
      </div>
      {reports.map((report) => (
        <Report
          key={report.id}
          report={report}
          task={codes.get(report.taskId) ?? ""}
        />
      ))}
    </section>
  );
});

const Report = memo(function Report({
  report,
  task,
}: {
  report: Block;
  task: string;
}) {
  const markdown = !report.mediaType || report.mediaType === "text/markdown";
  const at = report.updateTime ?? report.createTime;
  return (
    <article className="report-card" id={`report-${report.id}`}>
      <header className="report-head">
        {report.icon ? (
          <span className="report-icon" aria-hidden="true">
            {report.icon}
          </span>
        ) : (
          <FileText size={16} aria-hidden="true" className="report-icon" />
        )}
        <div>
          <h3>{report.title || t("agents.untitled")}</h3>
          <span className="eyebrow">
            {task ? t("reports.by_task", { task }) : t("reports.by_lead")}
            {at && ` · ${when(at)}`}
          </span>
        </div>
      </header>
      <div className="report-body">
        {markdown ? (
          <FoldedMarkdown
            text={report.content}
            more={t("reports.open")}
            less={t("reports.close")}
            copy
          />
        ) : report.mediaType === "image/svg+xml" ? (
          <SafeSvg source={report.content} />
        ) : (
          <pre className="wish-block-raw">{report.content}</pre>
        )}
      </div>
    </article>
  );
});
