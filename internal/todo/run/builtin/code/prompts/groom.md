This is a GROOMING pass over one backlog item: read the code, change nothing, and say whether the item is still true.

{{item}}

Take every claim the item makes and check it against the tree as it stands: every `path:line`, every function, flag, config key or command it names, every sentence about what happens today, every entry in `depends_on`, every acceptance criterion, and the size it is graded at. Read the files; do not answer from the item alone.

Ask one more thing of the acceptance criteria: whether the item changes what the model reads — a tool definition's schema or description, a toolbox line, a prompt paragraph — and, if it does, whether a criterion begins `The model is told:`. A criterion that names the channel without that opening is `changed`, with `now:` the same line opening that way; an item that changes what the model reads and has no criterion naming the channel at all is `unknown` on its acceptance-criteria heading, with the channel in the evidence.
