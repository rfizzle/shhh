"""The scripted endpoint's answer to the flows it knows on its own.

Run by `make tui-check` ahead of the scenes: python3 -m unittest discover -s
scripts/tui -p '*_test.py'.
"""
import unittest

import fakeprovider as fp


def request(*tools):
    """A request offering the named tools, in the dialect's own shape."""
    return {"messages": [{"role": "user", "content": "UNTRUSTED EXCHANGE:\n..."}],
            "tools": [{"type": "function", "function": {"name": t}} for t in tools],
            "tool_choice": "auto"}


class Flows(unittest.TestCase):
    def use(self, queues):
        fp.QUEUES = queues
        fp.QUEUED = "" not in queues
        fp.turn.clear()
        fp.children.clear()

    def test_a_title_request_is_answered_by_the_harness(self):
        self.use({"session": [["Hello."]], "reading": [["tool:session_summary:{}"]]})
        queue, _, parts, note = fp.answer(request("session_title"))
        self.assertEqual(queue, "title")
        self.assertEqual(parts, fp.FLOWS["session_title"][1])
        self.assertIn("harness", note)
        # The reading queue is untouched: its first reply is still the next.
        self.assertEqual(fp.answer(request("session_summary"))[2], ["tool:session_summary:{}"])

    def test_an_account_request_is_answered_by_the_harness_in_a_file_with_no_queues(self):
        self.use({"": [["The turn's own reply."]]})
        _, _, parts, _ = fp.answer(request("session_account"))
        self.assertEqual(parts, fp.FLOWS["session_account"][1])
        self.assertEqual(fp.answer(request("read_file", "write_file"))[2], ["The turn's own reply."])

    def test_a_scene_that_scripts_the_flow_wins(self):
        mine = ['tool:session_title:{"title":"The retry backoff doubling"}']
        self.use({"session": [["Hello."]], "title": [mine]})
        queue, _, parts, note = fp.answer(request("session_title"))
        self.assertEqual((queue, parts, note), ("title", mine, ""))
        # The account has no queue of its own, so the harness still answers it.
        self.assertEqual(fp.answer(request("session_account"))[2], fp.FLOWS["session_account"][1])

    def test_a_request_that_lost_its_tool_is_answered_from_the_queues(self):
        self.use({"session": [["Hello."]], "reading": [["tool:session_summary:{}"]]})
        queue, _, parts, _ = fp.answer(request())
        self.assertEqual((queue, parts), ("reading", ["tool:session_summary:{}"]))
        self.use({"": [["The turn's own reply."]]})
        self.assertEqual(fp.answer(request())[2], ["The turn's own reply."])

    def test_a_suggestion_request_is_answered_with_no_offer_by_the_harness(self):
        self.use({"session": [["Hello."]]})
        body = {"messages": [{"role": "system", "content": "You suggest..."},
                             {"role": "user", "content": 'UNTRUSTED EVIDENCE:\n{"last_instruction":"go"}'}]}
        queue, _, parts, note = fp.answer(body)
        self.assertEqual((queue, parts), ("suggestion", [""]))
        self.assertIn("harness", note)
        # The session's own queue is untouched.
        self.assertEqual(fp.turn.get("session", 0), 0)

    def test_a_scene_that_scripts_the_suggestion_wins(self):
        self.use({"session": [["Hello."]], "suggestion": [["Run the tests again."]]})
        body = {"messages": [{"role": "user", "content": 'UNTRUSTED EVIDENCE:\n{"last_instruction":"go"}'}]}
        self.assertEqual(fp.answer(body)[2], ["Run the tests again."])

    def test_a_turn_that_quotes_the_field_is_not_the_suggestion(self):
        self.use({"session": [["Hello."]]})
        body = {"messages": [{"role": "user", "content": 'what does "last_instruction" mean?'}],
                "tools": [{"type": "function", "function": {"name": "read_file"}}]}
        self.assertEqual(fp.flow(body), "")

    def test_the_tool_among_others_is_not_the_flow(self):
        self.use({"session": [["Hello."]]})
        self.assertEqual(fp.flow(request("session_title", "read_file")), "")


if __name__ == "__main__":
    unittest.main()
