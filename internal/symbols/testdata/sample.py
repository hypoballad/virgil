def add(a, b):
    """Adds two numbers."""
    return a + b

class Calculator:
    """A simple calculator class."""
    
    VERSION = "1.0"
    
    def __init__(self, initial_value=0):
        self.value = initial_value
        
    def increment(self):
        self.value += 1
        return self.value
        
    def get_value(self):
        return self.value

def top_level_func():
    # Local variable (should not be extracted)
    local_var = 10
    print(local_var)

GLOBAL_VAR = "hello"
PI = 3.14159
