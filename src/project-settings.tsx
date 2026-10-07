import { ProjectSetup, type ProjectSetupProps } from "./project-setup";
import "./project-settings.css";

export { ProjectSetup, ProjectSetupReport } from "./project-setup";

/**
 * The modal keeps this stable content-only API. The onboarding and settings
 * surface live in ProjectSetup so create and edit flows share the same state,
 * scan cancellation, report application, and save/error behavior.
 */
export type ProjectSettingsProps = ProjectSetupProps;

export function ProjectSettings(props: ProjectSettingsProps) {
  return <ProjectSetup {...props} />;
}

export default ProjectSettings;
