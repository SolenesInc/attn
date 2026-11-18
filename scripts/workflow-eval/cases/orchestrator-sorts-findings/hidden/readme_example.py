import ast
import re
import sys

from wordstats import top_words

readme = open("README.md").read()
call, shown = re.search(r">>> (top_words\(.*\))\n(.*)\n", readme).groups()
sys.exit(0 if eval(call) == ast.literal_eval(shown.strip()) else 1)
