import unittest

from inventory import restock, short


class RestockTest(unittest.TestCase):
    def test_counts_a_delivery(self):
        self.assertEqual(restock([("bolt", 4), ("nut", 2)]), {"bolt": 4, "nut": 2})

    def test_repeated_parts_add_up(self):
        self.assertEqual(restock([("bolt", 4), ("bolt", 3)]), {"bolt": 7})

    def test_each_shelf_starts_empty(self):
        restock([("bolt", 4)])
        self.assertEqual(restock([("nut", 1)]), {"nut": 1})

    def test_a_shelf_passed_in_is_added_to(self):
        shelf = {"bolt": 1}
        self.assertEqual(restock([("bolt", 2)], shelf), {"bolt": 3})


class ShortTest(unittest.TestCase):
    def test_names_what_is_missing(self):
        shelf = {"bolt": 4}
        self.assertEqual(short(shelf, {"bolt": 6, "nut": 2}), {"bolt": 2, "nut": 2})

    def test_a_full_shelf_is_short_of_nothing(self):
        self.assertEqual(short({"bolt": 6}, {"bolt": 6}), {})


if __name__ == "__main__":
    unittest.main()
