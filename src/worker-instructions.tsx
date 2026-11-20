// The workers' instructions by kind of task (W114, decision Q70 B): a general text, and one for each kind that wants
// its own (work, investigation, mockup, ifrit), in three layers resolved kind by kind: the Summoning Circle's, this
// machine's default for every project; the repository's .agents/djinn.yaml, which wins over it; your own settings of
// the project, which win over both. A kind no layer sets takes the general text. The Circle edits its layer
// (CircleInstructions), a project's settings page shows the three and edits yours (ProjectInstructions).
import { useState } from "react";

import {
  InstructionKind,
  type ProjectServiceShowResponse,
  SettingSource,
  type WorkerInstruction,
  type WorkerInstructions,
} from "../gen/ts/plan/v1/plan_pb";
import { message } from "./data/client";
import { t, type TextKey } from "./i18n";
import { From } from "./project-panel";
import { noShape, TextField, useTyped } from "./text-field";

// INSTRUCTIONS_MAX is the longest text of a kind, as the proto bounds it.
export const INSTRUCTIONS_MAX = 2000;

// InstructionSpec is a text of the instructions: its kind, its field in WorkerInstructions, how it shows.
interface InstructionSpec {
  readonly kind: InstructionKind;
  readonly field: "general" | "work" | "investigation" | "mockup" | "ifrit";
  readonly label: TextKey;
  readonly detail: TextKey;
}

// INSTRUCTION_KINDS are the texts in the order Djinn lists them: the general one first.
export const INSTRUCTION_KINDS: readonly InstructionSpec[] = [
  {
    kind: InstructionKind.GENERAL,
    field: "general",
    label: "instructions.kind_general",
    detail: "instructions.kind_general_detail",
  },
  {
    kind: InstructionKind.WORK,
    field: "work",
    label: "instructions.kind_work",
    detail: "instructions.kind_work_detail",
  },
  {
    kind: InstructionKind.INVESTIGATION,
    field: "investigation",
    label: "instructions.kind_investigation",
    detail: "instructions.kind_investigation_detail",
  },
  {
    kind: InstructionKind.MOCKUP,
    field: "mockup",
    label: "instructions.kind_mockup",
    detail: "instructions.kind_mockup_detail",
  },
  {
    kind: InstructionKind.IFRIT,
    field: "ifrit",
    label: "instructions.kind_ifrit",
    detail: "instructions.kind_ifrit_detail",
  },
];

// InstructionEditor is the field of one text: what you type stays the field's own (W526), read when you save it.
function InstructionEditor({
  label,
  initial,
  placeholder,
  busy,
  onSave,
  onUnset,
  onCancel,
}: {
  label: string;
  initial: string;
  placeholder: string;
  busy: boolean;
  onSave: (text: string) => void;
  // Shown when the layer sets the text: removes it.
  onUnset?: () => void;
  onCancel?: () => void;
}) {
  const typed = useTyped(noShape, initial);
  return (
    <div className="instruction-editor">
      <TextField
        className="instruction-field"
        text={typed.text}
        onText={typed.type}
        rows={2}
        maxLength={INSTRUCTIONS_MAX}
        aria-label={label}
        placeholder={placeholder}
        disabled={busy}
      />
      <div className="instruction-actions">
        <button
          type="button"
          className="button small"
          disabled={busy}
          onClick={() => onSave(typed.read())}
        >
          {t("instructions.save")}
        </button>
        {onUnset && (
          <button
            type="button"
            className="button secondary small"
            disabled={busy}
            onClick={onUnset}
          >
            {t("instructions.unset")}
          </button>
        )}
        {onCancel && (
          <button
            type="button"
            className="button secondary small"
            disabled={busy}
            onClick={onCancel}
          >
            {t("instructions.cancel")}
          </button>
        )}
      </div>
    </div>
  );
}

// SetInstruction sets a text of a layer, or removes it with unset.
export type SetInstruction = (req: {
  kind: InstructionKind;
  text: string;
  unset: boolean;
}) => Promise<void>;

// CircleInstructions is the Summoning Circle's tab of the workers' instructions: this machine's text for each kind,
// the default of every project.
export function CircleInstructions({
  instructions,
  onSet,
}: {
  instructions: WorkerInstructions | undefined;
  onSet: SetInstruction;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const set = (kind: InstructionKind, text: string, unset: boolean) => {
    setBusy(true);
    setError("");
    onSet({ kind, text, unset })
      .catch((err) => setError(message(err)))
      .finally(() => setBusy(false));
  };
  return (
    <>
      <p className="agent-setup-intro">{t("instructions.circle_detail")}</p>
      {error && (
        <p className="field-error" role="alert">
          {error}
        </p>
      )}
      <section className="config-box instructions-list">
        {INSTRUCTION_KINDS.map((spec) => {
          const value = instructions?.[spec.field];
          return (
            <div
              className="instruction-row"
              key={spec.field}
              data-instruction={spec.field}
            >
              <strong>{t(spec.label)}</strong>
              <p>{t(spec.detail)}</p>
              <InstructionEditor
                key={value ?? ""}
                label={t(spec.label)}
                initial={value ?? ""}
                placeholder={t(
                  spec.kind === InstructionKind.GENERAL
                    ? "instructions.none_general"
                    : "instructions.none_kind",
                )}
                busy={busy}
                onSave={(text) => set(spec.kind, text, false)}
                onUnset={
                  value !== undefined
                    ? () => set(spec.kind, "", true)
                    : undefined
                }
              />
            </div>
          );
        })}
      </section>
    </>
  );
}

// ProjectInstructions is a project's instructions by kind, as Djinn retains them: each text in the Summoning Circle,
// the repository's djinn.yaml and your own file, the one retained and where it comes from; a lower layer a higher one
// overrides is struck. You edit yours here; the repository's goes through a commit, the Circle's in the Circle.
export function ProjectInstructions({
  shown,
  onSet,
}: {
  shown: ProjectServiceShowResponse;
  onSet: SetInstruction;
}) {
  // The kind whose text of yours is open to edit.
  const [editing, setEditing] = useState<InstructionKind>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const set = (kind: InstructionKind, text: string, unset: boolean) => {
    setBusy(true);
    setError("");
    onSet({ kind, text, unset })
      .then(() => setEditing(undefined))
      .catch((err) => setError(message(err)))
      .finally(() => setBusy(false));
  };
  const side = (value: string | undefined, struck: boolean) =>
    value === undefined ? (
      <span className="setting-absent">{t("project.value_absent")}</span>
    ) : (
      <span className={`instruction-text${struck ? " setting-struck" : ""}`}>
        {value || t("project.setting_empty")}
      </span>
    );
  return (
    <section className="config-box settings-box">
      <h3>
        {t("instructions.project_title")}
        <span className="config-from">{t("instructions.project_where")}</span>
      </h3>
      <p className="settings-lead">{t("instructions.project_detail")}</p>
      {error && (
        <p className="field-error" role="alert">
          {error}
        </p>
      )}
      <table className="settings-table instructions-table">
        <thead>
          <tr>
            <th scope="col">{t("instructions.col_kind")}</th>
            <th scope="col">{t("instructions.col_machine")}</th>
            <th scope="col">{t("project.col_repository")}</th>
            <th scope="col">{t("project.col_local")}</th>
            <th scope="col">{t("project.col_retained")}</th>
          </tr>
        </thead>
        {INSTRUCTION_KINDS.map((spec) => {
          const row: WorkerInstruction | undefined = shown.instructions.find(
            (r) => r.kind === spec.kind,
          );
          if (!row) return null;
          const open = editing === spec.kind;
          return (
            <tbody
              key={spec.field}
              className="setting-group"
              data-instruction={spec.field}
            >
              <tr>
                <th scope="row">
                  <span className="setting-label">{t(spec.label)}</span>
                  <code className="setting-key">{spec.field}</code>
                </th>
                <td data-side="machine">
                  {side(
                    row.machine,
                    row.repository !== undefined || row.developer !== undefined,
                  )}
                </td>
                <td data-side="repository">
                  {side(row.repository, row.developer !== undefined)}
                </td>
                <td data-side="local">
                  {side(row.developer, false)}
                  {!open && (
                    <button
                      type="button"
                      className="text-button instruction-edit"
                      disabled={busy}
                      onClick={() => setEditing(spec.kind)}
                    >
                      {row.developer === undefined
                        ? t("instructions.set_mine")
                        : t("instructions.edit_mine")}
                    </button>
                  )}
                </td>
                <td data-side="retained">
                  {row.source === SettingSource.DEFAULT ? (
                    <span className="setting-absent">
                      {t("instructions.none")}
                    </span>
                  ) : (
                    <span className="setting-retained instruction-text">
                      {row.text || t("project.setting_empty")}
                    </span>
                  )}
                  {row.general ? (
                    <span className="config-from" data-general="">
                      {t("instructions.takes_general")}
                    </span>
                  ) : (
                    row.source !== SettingSource.DEFAULT && (
                      <From source={row.source} shown={shown} />
                    )
                  )}
                </td>
              </tr>
              {open && (
                <tr className="setting-note">
                  <td colSpan={5}>
                    <InstructionEditor
                      label={t("instructions.mine_label", {
                        kind: t(spec.label),
                      })}
                      initial={row.developer ?? ""}
                      placeholder={t("instructions.mine_placeholder")}
                      busy={busy}
                      onSave={(text) => set(spec.kind, text, false)}
                      onUnset={
                        row.developer !== undefined
                          ? () => set(spec.kind, "", true)
                          : undefined
                      }
                      onCancel={() => setEditing(undefined)}
                    />
                  </td>
                </tr>
              )}
            </tbody>
          );
        })}
      </table>
    </section>
  );
}
