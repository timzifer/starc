# Fibonacci numbers, exported for the host and printed at start-up.

def fib(n):
    """Returns the n'th Fibonacci number."""
    a, b = 0, 1
    for _ in range(n):
        a, b = b, a + b
    return a

def fib_table(n):
    return {i: fib(i) for i in range(n)}

print("fib(30) =", fib(30))
