# Security policy

## Reporting a vulnerability

Please do not open a public issue for a security problem. Report it
privately through GitHub's vulnerability reporting form:

https://github.com/ChristopherDavenport/agentimport/security/advisories/new

Include the affected version, a description of the issue and, if you
have one, a minimal reproduction. You will get an acknowledgement within
a week. Once a fix is available it ships as a new patch version and the
advisory is published with credit to the reporter, unless you prefer to
stay anonymous.

## Supported versions

Only the latest release receives security fixes.

## Scope

This module reads session logs written by other programs, which may
come from other machines, and writes session files that hold whole
conversations, tool arguments and tool output. Anything that lets a
crafted log crash a process, exhaust its memory, or make the command
write outside the output directory it was given through a session ID
or path is in scope.
