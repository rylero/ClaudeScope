class ClaudeScopeError(Exception):
    """Raised when the ClaudeScope CLI/daemon reports a failure.

    `code` mirrors the machine-readable code the Go CLI puts in its
    `{"error": ..., "code": ...}` JSON payload (e.g. "NO_SESSION",
    "QUERY_ERROR", "DAEMON_UNAVAILABLE").
    """

    def __init__(self, message: str, code: str = "UNKNOWN"):
        super().__init__(message)
        self.code = code

    def __repr__(self) -> str:
        return f"ClaudeScopeError(code={self.code!r}, message={str(self)!r})"
