"""Starter API — Python (Flask, served by waitress) on the platform's MySQL.

The platform runs `python app.py`, injects PORT (the port it routes to) and,
because deployment.yaml declares `database: type: mysql`, the connection
details as DATABASE_HOST / DATABASE_PORT / DATABASE_NAME / DATABASE_USER /
DATABASE_PASSWORD. Nothing here is configured by hand.
"""

import os
import threading
import time

import pymysql
from flask import Flask, jsonify, request
from waitress import serve

app = Flask(__name__)
_ready = threading.Event()


def connect():
    return pymysql.connect(
        host=os.environ.get("DATABASE_HOST", "127.0.0.1"),
        port=int(os.environ.get("DATABASE_PORT", "3306")),
        user=os.environ.get("DATABASE_USER", "appuser"),
        password=os.environ.get("DATABASE_PASSWORD", ""),
        database=os.environ.get("DATABASE_NAME", "appdb"),
        cursorclass=pymysql.cursors.DictCursor,
        autocommit=True,
        connect_timeout=5,
    )


def init_schema():
    # The database can still be starting when the API does; keep trying
    # rather than exit, so the API is healthy either way.
    while not _ready.is_set():
        try:
            with connect() as conn, conn.cursor() as cur:
                cur.execute(
                    "CREATE TABLE IF NOT EXISTS items ("
                    " id INT AUTO_INCREMENT PRIMARY KEY,"
                    " title VARCHAR(200) NOT NULL,"
                    " created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP)"
                )
            _ready.set()
        except pymysql.MySQLError as exc:
            print(f"database not ready yet: {exc}", flush=True)
            time.sleep(2)


@app.get("/")
def health():
    return jsonify(status="ok", runtime="python", database="connected" if _ready.is_set() else "unavailable")


def like_pattern(q):
    """Matches q anywhere, with LIKE's own wildcards taken literally."""
    return "%" + q.replace("\\", "\\\\").replace("%", "\\%").replace("_", "\\_") + "%"


# RESTful: the collection is /items, one item /items/<id>, and a search is a
# query parameter on the collection (GET /items?q=...).
@app.get("/items")
def list_items():
    q = request.args.get("q", "").strip()
    with connect() as conn, conn.cursor() as cur:
        if q:
            cur.execute("SELECT id, title, created_at FROM items WHERE title LIKE %s ORDER BY id", (like_pattern(q),))
        else:
            cur.execute("SELECT id, title, created_at FROM items ORDER BY id")
        rows = cur.fetchall()
    for row in rows:
        row["created_at"] = row["created_at"].isoformat()
    return jsonify(rows)


@app.get("/items/<int:item_id>")
def get_item(item_id):
    with connect() as conn, conn.cursor() as cur:
        cur.execute("SELECT id, title, created_at FROM items WHERE id = %s", (item_id,))
        row = cur.fetchone()
    if row is None:
        return jsonify(error="item not found"), 404
    row["created_at"] = row["created_at"].isoformat()
    return jsonify(row)


@app.post("/items")
def create_item():
    title = str((request.get_json(silent=True) or {}).get("title", "")).strip()
    if not title:
        return jsonify(error="title is required"), 400
    with connect() as conn, conn.cursor() as cur:
        cur.execute("INSERT INTO items (title) VALUES (%s)", (title[:200],))
        new_id = cur.lastrowid
    return jsonify(id=new_id, title=title[:200]), 201


if __name__ == "__main__":
    threading.Thread(target=init_schema, daemon=True).start()
    port = int(os.environ.get("PORT", "8080"))
    print(f"listening on :{port}", flush=True)
    serve(app, host="0.0.0.0", port=port)
