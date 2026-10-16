# Watcher

A watcher is a task that runs a command, with no agent and no model: it follows a pipeline, a pull request, a queue.
Each new paragraph it prints wakes the Djinn. A watcher runs a command only when the project allows it: listed in its
permissions, declared by one of its skills, or allowed by you in the project's panel.

When a task opens a pull or merge request in a project that names its babysit skill, Djinn starts a watcher on it by
itself.

> **This page is a sketch.** Its full guide, how to use it from the window and from the Djinn, what to expect and
> the common mistakes, is being written.

## Read next

Related: [Pull and merge requests](#pull-requests), [Skills and templates](#skills-and-templates).
