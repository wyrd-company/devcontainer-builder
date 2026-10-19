# relationships:
#   references: [compatibility, run]
"""Git subprocess environment for release verification and test fixtures."""

import os


def git_env(*, fixture=False):
    env = {
        key: value for key, value in os.environ.items() if not key.startswith("GIT_")
    }
    if fixture:
        env.update({
            "GIT_AUTHOR_NAME": "Example",
            "GIT_AUTHOR_EMAIL": "example@example.invalid",
            "GIT_COMMITTER_NAME": "Example",
            "GIT_COMMITTER_EMAIL": "example@example.invalid",
            "GIT_CONFIG_GLOBAL": "/dev/null",
            "GIT_CONFIG_NOSYSTEM": "1",
        })
    return env
