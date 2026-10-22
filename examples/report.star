# Loads a library: starc run examples/report.star summary '[3, 5, 10]'
load("lib/stats.star", "mean")

def summary(xs):
    return {"n": len(xs), "mean": mean(xs)}
