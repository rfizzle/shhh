package run

// The built-in stage instructions, named so a surface outside this package
// can write one to a file or compare one against a file that was written.
//
// They are accessors rather than exported constants because what a stage is
// told is this package's to shape: the blocks it places, the answer shape it
// appends, the substitutions it reads back. A caller gets the wording as it
// stands and nothing to assign to.
//
// A scaffold writes these verbatim, which is what makes editing a stage
// prompt an edit rather than a search through Go for the text and a key to
// point at it. The same texts are what a wording is compared against to
// decide whether it replaced anything at all: a file holding exactly the
// built-in asks the model exactly what the built-in asks, and a record that
// split the runs either side of it would report a change nobody made.
// See docs/capabilities/todo.md#the-stage-prompts-are-yours-to-edit.

// BuiltinWordings is the built-in set, in the order the settings table
// declares the keys that replace them. It is a Wordings rather than seven
// functions so a caller that walks the set cannot pair a stage with another
// stage's text.
func BuiltinWordings() Wordings {
	return Wordings{
		Standards:  builtinStandards,
		Research:   builtinResearch,
		Implement:  builtinImplement,
		Review:     builtinReview,
		ReviewTask: builtinReviewTask,
		Remediate:  builtinRemediate,
		Commit:     builtinCommit,
	}
}
