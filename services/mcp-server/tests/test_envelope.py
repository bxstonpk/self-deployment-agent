from mcp_server.envelope import ErrorCode, ErrorDetail, error, pending_approval, success


def test_success_envelope_shape():
    env = success({"foo": "bar"})
    assert env["status"] == "success"
    assert env["data"] == {"foo": "bar"}
    assert env["error"] is None
    assert env["request_id"]
    assert env["server_time"]


def test_error_envelope_shape():
    env = error(ErrorCode.NOT_FOUND, "nope", details=[ErrorDetail(field="id", reason="missing")])
    assert env["status"] == "error"
    assert env["data"] is None
    assert env["error"]["code"] == "NOT_FOUND"
    assert env["error"]["message"] == "nope"
    assert env["error"]["details"] == [{"field": "id", "reason": "missing"}]


def test_pending_approval_is_a_success_envelope_not_an_error():
    env = pending_approval({"deployment_id": "d1"})
    assert env["status"] == "success"
    assert env["data"]["deployment_id"] == "d1"
    assert env["data"]["mcp_status"] == "PENDING_APPROVAL"
    assert env["error"] is None


def test_request_ids_are_unique_across_calls():
    a = success({})
    b = success({})
    assert a["request_id"] != b["request_id"]
