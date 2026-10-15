import { ListChecks, MessageSquare, ScrollText } from "lucide-react";
import { type KeyboardEvent, type ReactNode } from "react";

import "./wish-activity.css";

export type ActivityTab = "decisions" | "instructions" | "journal";

export interface WishActivityTab {
  id: ActivityTab;
  label: ReactNode;
  /** A compact icon used by the workspace panel header. */
  icon?: ReactNode;
  /** Accessible label for icon-only tab controls. */
  ariaLabel?: string;
  count?: number;
  content: ReactNode;
}

function tabLabel(tab: WishActivityTab): string {
  if (tab.ariaLabel) return tab.ariaLabel;
  if (typeof tab.label === "string") return tab.label;
  return tab.id;
}

function activityIcon(id: ActivityTab): ReactNode {
  if (id === "journal")
    return <ScrollText size={16} strokeWidth={1.8} aria-hidden="true" />;
  if (id === "decisions")
    return <ListChecks size={16} strokeWidth={1.8} aria-hidden="true" />;
  return <MessageSquare size={16} strokeWidth={1.8} aria-hidden="true" />;
}

/** A zero-count activity kind has no workspace content to navigate to. An omitted count stays visible while data loads. */
export function visibleActivityTabs(
  tabs: readonly WishActivityTab[],
): WishActivityTab[] {
  return tabs.filter((tab) => tab.count !== 0);
}

function selectedActivityTab(
  tabs: readonly WishActivityTab[],
  activeTab: ActivityTab,
): ActivityTab | undefined {
  return tabs.find((tab) => tab.id === activeTab)?.id ?? tabs[0]?.id;
}

export function WishActivityTabs({
  label,
  tabs,
  activeTab,
  onTabChange,
  iconsOnly = false,
}: {
  label: string;
  tabs: readonly WishActivityTab[];
  activeTab: ActivityTab;
  onTabChange: (tab: ActivityTab) => void;
  iconsOnly?: boolean;
}) {
  const visibleTabs = visibleActivityTabs(tabs);
  const selectedTab = selectedActivityTab(visibleTabs, activeTab);
  if (!selectedTab) return null;

  return (
    <div
      className={`wish-activity-tabs${iconsOnly ? " is-icon-only" : ""}`}
      role="tablist"
      aria-label={label}
    >
      {visibleTabs.map((tab) => {
        const text = tabLabel(tab);
        const accessibleLabel =
          tab.count === undefined ? text : `${text} · ${tab.count}`;
        return (
          <button
            key={tab.id}
            type="button"
            role="tab"
            id={`activity-tab-${tab.id}`}
            aria-label={iconsOnly ? accessibleLabel : undefined}
            aria-selected={selectedTab === tab.id}
            aria-controls={`activity-panel-${tab.id}`}
            tabIndex={selectedTab === tab.id ? 0 : -1}
            className={selectedTab === tab.id ? "is-active" : ""}
            title={iconsOnly ? accessibleLabel : undefined}
            onClick={() => onTabChange(tab.id)}
            onKeyDown={(event: KeyboardEvent<HTMLButtonElement>) => {
              const index = visibleTabs.findIndex((item) => item.id === tab.id);
              const next =
                event.key === "ArrowRight" || event.key === "ArrowDown"
                  ? (index + 1) % visibleTabs.length
                  : event.key === "ArrowLeft" || event.key === "ArrowUp"
                    ? (index - 1 + visibleTabs.length) % visibleTabs.length
                    : event.key === "Home"
                      ? 0
                      : event.key === "End"
                        ? visibleTabs.length - 1
                        : undefined;
              if (next === undefined) return;
              event.preventDefault();
              const nextTab = visibleTabs[next];
              onTabChange(nextTab.id);
              document
                .getElementById(`activity-tab-${nextTab.id}`)
                ?.focus({ preventScroll: true });
            }}
          >
            {iconsOnly ? (
              <span className="wish-activity-icon" aria-hidden="true">
                {activityIcon(tab.id)}
              </span>
            ) : (
              tab.icon
            )}
            <span className={iconsOnly ? "visually-hidden" : undefined}>
              {tab.label}
            </span>
            {tab.count !== undefined && !iconsOnly && (
              <span className="count">{tab.count}</span>
            )}
          </button>
        );
      })}
    </div>
  );
}

export function WishActivity({
  label,
  tabs,
  activeTab,
  onTabChange,
  hideTabs = false,
}: {
  label: string;
  tabs: readonly WishActivityTab[];
  activeTab: ActivityTab;
  onTabChange: (tab: ActivityTab) => void;
  hideTabs?: boolean;
}) {
  const visibleTabs = visibleActivityTabs(tabs);
  const selectedTab = selectedActivityTab(visibleTabs, activeTab);
  if (!selectedTab) return null;

  return (
    <section className="wish-activity" aria-label={label}>
      {!hideTabs && (
        <WishActivityTabs
          label={label}
          tabs={visibleTabs}
          activeTab={selectedTab}
          onTabChange={onTabChange}
        />
      )}
      {visibleTabs.map((tab) => (
        <div
          key={tab.id}
          id={`activity-panel-${tab.id}`}
          className="wish-activity-panel"
          role="tabpanel"
          aria-labelledby={`activity-tab-${tab.id}`}
          hidden={selectedTab !== tab.id}
        >
          {tab.content}
        </div>
      ))}
    </section>
  );
}
