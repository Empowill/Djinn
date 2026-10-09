// The inbox: what the sources of the projects' skills printed (a merge request assigned to you, a mention), each
// item a card with the route Djinn proposes, the recommended destination first. Nothing is made until you click: a
// destination files the item or makes its wish, "Rub the lamp" takes the recommended one, "Dismiss" sets it aside.
// Djinn never answers the source. Empty means hidden.
import { Inbox as InboxIcon, Lamp, X } from "lucide-react";
import { useState } from "react";

import {
  Change,
  type InboxItem,
  type RouteOption,
  RouteKind,
} from "../gen/ts/plan/v1/plan_pb";
import { message } from "./data/client";
import { useClients, useData, useStore } from "./data/djinn";
import { choiceOf, letter, when } from "./data/format";
import { t } from "./i18n";
import "./inbox.css";

export function Inbox({
  onOpen,
  onToast,
}: {
  // Shows the wish an item went to.
  onOpen: (wishId: string) => void;
  onToast: (text: string) => void;
}) {
  const items = useData((s) => s.inbox);
  if (!items.length) return null;
  return (
    <section className="inbox" aria-label={t("inbox.title")}>
      <div className="section-title">
        <h2>
          <InboxIcon size={17} aria-hidden="true" />
          {t("inbox.title")}
          <span className="count">{items.length}</span>
        </h2>
        <p>{t("inbox.detail")}</p>
      </div>
      {items.map((item) => (
        <InboxCard
          key={item.id}
          item={item}
          onOpen={onOpen}
          onToast={onToast}
        />
      ))}
    </section>
  );
}

// optionText is how a destination reads, as on the card of a routed request.
function optionText(
  opt: RouteOption,
  names: ReadonlyMap<string, string>,
): string {
  const skill = opt.template
    ? t("route.with_skill", { skill: opt.template.skill })
    : "";
  const params = {
    wish: opt.title,
    title: opt.title,
    pause: names.get(opt.pauseWishId) ?? "",
  };
  switch (opt.kind) {
    case RouteKind.FILE:
      return t("route.file", params);
    case RouteKind.QUEUE:
      return t("route.queue", params) + skill;
    case RouteKind.SWAP:
      return t("route.swap", params) + skill;
  }
  const projects = opt.projectIds
    .map((id) => names.get(id))
    .filter(Boolean)
    .join(", ");
  return (
    (projects
      ? t("route.new", { ...params, projects })
      : t("route.new_bare", params)) + skill
  );
}

function InboxCard({
  item,
  onOpen,
  onToast,
}: {
  item: InboxItem;
  onOpen: (wishId: string) => void;
  onToast: (text: string) => void;
}) {
  const clients = useClients();
  const store = useStore();
  const projects = useData((s) => s.projects);
  const wishes = useData((s) => s.wishes);
  const names = new Map<string, string>([
    ...projects.map((p) => [p.id, p.name] as [string, string]),
    ...wishes.map((w) => [w.id, w.title] as [string, string]),
  ]);
  const [sending, setSending] = useState(false);
  const [first, ...rest] = item.text.split("\n");
  const options = item.route?.options ?? [];

  const send = async (run: () => Promise<void>) => {
    setSending(true);
    try {
      await run();
    } catch (error) {
      onToast(message(error));
    } finally {
      setSending(false);
      void store.changed("", [Change.INBOX, Change.WISH]);
    }
  };
  const route = (index: number) =>
    send(async () => {
      const res = await clients.inbox.route({
        itemId: item.id,
        choice: choiceOf(index),
      });
      const wishId = res.item?.wishId;
      if (wishId) {
        await store.changed(wishId, [Change.WISH, Change.BLOCK]);
        onOpen(wishId);
      }
    });
  const dismiss = () =>
    send(async () => {
      await clients.inbox.dismiss({ itemId: item.id });
      onToast(t("inbox.dismissed"));
    });

  return (
    <article className="question-card open inbox-card">
      <div className="question-top">
        <div className="question-heading">
          <div className="question-title">
            <span className="question-meta">
              <span className="inbox-source">
                {t("inbox.from", { source: item.source })}
              </span>
              <span className="inbox-when">{when(item.createTime)}</span>
            </span>
            <h3>{first}</h3>
            {rest.length > 0 && <p className="inbox-rest">{rest.join("\n")}</p>}
          </div>
        </div>
      </div>
      <div className="question-inner">
        <div
          className="question-options"
          role="group"
          aria-label={t("inbox.routes")}
        >
          {options.map((opt, index) => (
            <button
              key={index}
              className={`option ${index === 0 ? "selected" : ""}`}
              disabled={sending}
              title={opt.reason}
              onClick={() => void route(index)}
            >
              <strong className="option-letter">{letter(index)}</strong>
              <p>{optionText(opt, names)}</p>
              {index === 0 && (
                <span className="option-recommended">
                  {t("question.recommended")}
                </span>
              )}
            </button>
          ))}
        </div>
        <div className="question-actions">
          {options.length > 0 && (
            <button
              className="button accent lamp-rub"
              title={t("inbox.rub_detail")}
              disabled={sending}
              onClick={() => void route(0)}
            >
              <Lamp size={14} />
              {t("question.rub")}
            </button>
          )}
          <span className="spacer" />
          <button
            className="button secondary small"
            disabled={sending}
            onClick={() => void dismiss()}
          >
            <X size={14} />
            {t("inbox.dismiss")}
          </button>
        </div>
        <p className="question-hint">{t("inbox.reads_only")}</p>
      </div>
    </article>
  );
}
