  .linters.exclusions.rules |= [(. // [])[], {
    "linters": [
        "paralleltest", # ginkgo has its own parallelism
        "mnd", # test code deliberately defines many magic numbers
        "wrapcheck", # test code does not need monitoring, error tagging is useless
        "gosec", # test runs in safe sandbox and always has trusted inputs
        "tagliatelle" # we don't define anything in tests; we just consume existing structs
    ],
    "path": ".*"
}]
