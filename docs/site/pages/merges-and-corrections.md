# Merges and corrections

In a project whose settings name its checks, Djinn brings each task's finished work into the wish's branch by itself:
it merges the task's branch in a worktree of its own, runs the checks, and moves the branch when they pass. Each task
says where its work stands. A conflict in code, or a red check, starts a correction worker; past two attempts, Djinn
asks you.

> **This page is a sketch.** Its full guide, how to use it from the window and from the Djinn, what to expect and
> the common mistakes, is being written.

## Read next

Related: [Integration branch](#integration-branch), [Gates](#gates).
