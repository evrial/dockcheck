Fastest and simplest Docker image update checker.
No dependencies, only native Docker API calls.
Doing only one thing, and doing it well.

Building:
```
go mod tidy
go build -o dockcheck
```

Usage of ./dockcheck:
```
  -a    Automatic updates without interaction
  -e string
        Comma-separated list of container names to exclude
  -n    Check availability only
  -p    Auto-prune dangling images after update
  -s    Include stopped containers
  -t int
        Timeout in seconds per container lookup (default 10)
  -x int
        Max concurrent asynchronous lookups (default 10)
```