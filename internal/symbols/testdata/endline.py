def top_level():
    return "top"


class EndLineExample:
    def method(self, value):
        if value:
            return value
        return "empty"


def outer():
    def inner():
        return "inner"

    return inner()
