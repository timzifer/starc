# Word frequency counting over text supplied by the host.

def count_words(text, top = 3):
    counts = {}
    for word in text.lower().split():
        word = word.strip(".,;:!?\"'()")
        if word:
            counts[word] = counts.get(word, 0) + 1
    ranked = sorted(counts.items(), key = lambda kv: (-kv[1], kv[0]))
    return [{"word": w, "count": n} for w, n in ranked[:top]]
