  .issues.["exclude-rules"] |= [(. // [])[], {
    "linters": [
        "paralleltest",
        "mnd",
        "wrapcheck"
    ],
    "path": ".*"
}]
