#
# Copyright (c) 2018-2022, NVIDIA CORPORATION. All rights reserved.
#

# Default provider is AIS, so all Cloud-related tests are skipped.

import random
import string
import unittest
from aistore.client.errors import AISError, ErrBckNotFound
import tempfile

from aistore.client.api import Client
from . import CLUSTER_ENDPOINT

OBJ_READ_TYPE_ALL = "read_all"
OBJ_READ_TYPE_CHUNK = "chunk"


class TestObjectOps(unittest.TestCase):  # pylint: disable=unused-variable
    def setUp(self) -> None:
        letters = string.ascii_lowercase
        self.bck_name = ''.join(random.choice(letters) for _ in range(10))

        self.client = Client(CLUSTER_ENDPOINT)

    def tearDown(self) -> None:
        # Try to destroy bucket if there is one left.
        try:
            self.client.destroy_bucket(self.bck_name)
        except ErrBckNotFound:
            pass

    def _test_get_obj(self, read_type, obj_name, exp_content):
        chunk_size = random.randrange(1, len(exp_content) + 10)
        stream = self.client.get_object(self.bck_name, obj_name, chunk_size=chunk_size)
        self.assertEqual(stream.content_length, len(exp_content))
        self.assertTrue(stream.e_tag != "")
        if read_type == OBJ_READ_TYPE_ALL:
            obj = stream.read_all()
        else:
            obj = b''
            for chunk in stream:
                obj += chunk
        self.assertEqual(obj, exp_content)

    def test_put_head_get(self):
        self.client.create_bucket(self.bck_name)
        num_objs = 10

        for i in range(num_objs):
            s = "test string" * random.randrange(1, 10)
            content = s.encode('utf-8')
            obj_name = f"obj{ i }"
            with tempfile.NamedTemporaryFile() as f:
                f.write(content)
                f.flush()
                self.client.put_object(self.bck_name, obj_name, f.name)

            properties = self.client.head_object(self.bck_name, obj_name)
            self.assertEqual(properties['ais-version'], '1')
            self.assertEqual(properties['content-length'], str(len(content)))
            for option in [OBJ_READ_TYPE_ALL, OBJ_READ_TYPE_CHUNK]:
                self._test_get_obj(option, obj_name, content)

    def test_list_object_page(self):
        bucket_size = 110
        tests = [
            {
                "page_size": None, "resp_size": bucket_size
            },
            {
                "page_size": 7, "resp_size": 7
            },
            {
                "page_size": bucket_size * 2, "resp_size": bucket_size
            },
        ]
        self.client.create_bucket(self.bck_name)
        content = "test".encode("utf-8")
        with tempfile.NamedTemporaryFile() as f:
            f.write(content)
            f.flush()
            for obj_id in range(bucket_size):
                self.client.put_object(self.bck_name, f"obj-{ obj_id }", f.name)
        for test in list(tests):
            resp = self.client.list_objects(self.bck_name, page_size=test["page_size"])
            self.assertEqual(len(resp.entries), test["resp_size"])

    def test_list_all_objects(self):
        bucket_size = 110
        short_page_len = 17
        self.client.create_bucket(self.bck_name)
        content = "test".encode("utf-8")
        with tempfile.NamedTemporaryFile() as f:
            f.write(content)
            f.flush()
            for obj_id in range(bucket_size):
                self.client.put_object(self.bck_name, f"obj-{ obj_id }", f.name)
        objects = self.client.list_all_objects(self.bck_name)
        self.assertEqual(len(objects), bucket_size)
        objects = self.client.list_all_objects(self.bck_name, page_size=short_page_len)
        self.assertEqual(len(objects), bucket_size)

    def test_list_object_iter(self):
        bucket_size = 110
        self.client.create_bucket(self.bck_name)
        content = "test".encode("utf-8")
        objects = {}
        with tempfile.NamedTemporaryFile() as f:
            f.write(content)
            f.flush()
            for obj_id in range(bucket_size):
                obj_name = f"obj-{ obj_id }"
                self.client.put_object(self.bck_name, obj_name, f.name)
                objects[obj_name] = 1

        # Read all `bucket_size` objects by prefix.
        obj_iter = self.client.list_objects_iter(bck_name=self.bck_name, page_size=15, prefix="obj-")
        for obj in obj_iter:
            del objects[obj.name]
        self.assertEqual(len(objects), 0)

        # Empty iterator if there are no objects matching the prefix.
        obj_iter = self.client.list_objects_iter(bck_name=self.bck_name, prefix="invalid-obj-")
        for obj in obj_iter:
            objects[obj.name] = 1
        self.assertEqual(len(objects), 0)

    def test_obj_delete(self):
        bucket_size = 10
        delete_cnt = 7
        self.client.create_bucket(self.bck_name)
        content = "test".encode("utf-8")
        with tempfile.NamedTemporaryFile() as f:
            f.write(content)
            f.flush()
            for obj_id in range(bucket_size):
                self.client.put_object(self.bck_name, f"obj-{ obj_id }", f.name)
        objects = self.client.list_objects(self.bck_name)
        self.assertEqual(len(objects.entries), bucket_size)

        for obj_id in range(delete_cnt):
            self.client.delete_object(self.bck_name, f"obj-{ obj_id + 1 }")
        objects = self.client.list_objects(self.bck_name)
        self.assertEqual(len(objects.entries), bucket_size - delete_cnt)

    def test_empty_bucket(self):
        self.client.create_bucket(self.bck_name)
        objects = self.client.list_objects(self.bck_name)
        self.assertEqual(len(objects.entries), 0)

    def test_bucket_with_no_matching_prefix(self):
        bucket_size = 10
        self.client.create_bucket(self.bck_name)
        objects = self.client.list_objects(self.bck_name)
        self.assertEqual(len(objects.entries), 0)
        content = "test".encode("utf-8")
        with tempfile.NamedTemporaryFile() as f:
            f.write(content)
            f.flush()
            for obj_id in range(bucket_size):
                self.client.put_object(self.bck_name, f"obj-{ obj_id }", f.name)
        objects = self.client.list_objects(self.bck_name, prefix="TEMP")
        self.assertEqual(len(objects.entries), 0)

    def test_invalid_bck_name(self):
        with self.assertRaises(ErrBckNotFound):
            self.client.list_objects(bck_name="INVALID_BCK_NAME")

    def test_invalid_bck_name_for_aws(self):
        with self.assertRaises(AISError):
            self.client.list_objects(bck_name="INVALID_BCK_NAME", provider="aws")


if __name__ == '__main__':
    unittest.main()