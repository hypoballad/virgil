def main():
    greet("world")
    value = compute(10)
    print(value)


def greet(name):
    print("hello", name)


def compute(x):
    return double(x) + 1


def double(x):
    return x * 2


class Counter:
    def __init__(self):
        self.value = 0

    def increment(self):
        self.value += 1
        self.log()

    def log(self):
        print("count:", self.value)


def use_counter():
    c = Counter()
    c.increment()
    c.increment()
