# <application-name> — เอกสารสำหรับผู้อนุมัติ (REVIEW.md)

<!--
Claude: copy this file to the project root as REVIEW.md and fill in EVERY
section before deploying to an environment that waits for approval (see
get_platform_info's approval_required_environments). Write it in Thai unless
the employee asks otherwise. Be specific and honest: the approver reads this
next to every source file and signs both. Never put a secret VALUE here —
names only. Update it on every deployment; "What changed" is about this one.
Delete these comments.
-->

## 1. แอปนี้ทำอะไร (Purpose)

- ทำอะไร ใช้แก้ปัญหาอะไร:
- ใครใช้ (แผนก / จำนวนคนโดยประมาณ):
- เจ้าของ / ผู้ขอ deploy:

## 2. Workflow — ข้อมูลไหลผ่านระบบอย่างไร

อธิบายเส้นทางของ request ตั้งแต่ผู้ใช้เปิดหน้าเว็บจนถึงฐานข้อมูล เช่น

1. ผู้ใช้เปิด `http://<platform>/<application-name>/` → หน้า React (`frontend/src/App.jsx`)
2. หน้าเว็บเรียก `GET /<application-name>/api/items` → `api/...` (ไฟล์/ฟังก์ชันไหน)
3. API อ่าน/เขียนตาราง `items` ใน MySQL
4. ...

## 3. โครงสร้างโปรแกรมและ path ของทุกไฟล์ (Structure)

ทุกไฟล์ที่เขียนหรือแก้เอง (ไม่ต้องรวม lockfile ที่สร้างอัตโนมัติ) พร้อมหน้าที่:

| Path | หน้าที่ |
|---|---|
| `deployment.yaml` | การตั้งค่าการ deploy (services, database) |
| `frontend/src/App.jsx` | |
| `frontend/src/api.js` | |
| `api/...` | |

## 4. ข้อมูลที่เก็บ (Data)

| ตาราง | คอลัมน์ | เก็บอะไร | มีข้อมูลส่วนบุคคลไหม |
|---|---|---|---|
| | | | |

## 5. การเรียกออกนอกแอป (External calls)

- บริการภายนอก / URL ที่เรียก (ถ้าไม่มีให้เขียนว่า "ไม่มี"):

## 6. Configuration และ secret ที่ใช้ (ชื่อเท่านั้น ห้ามใส่ค่า)

| ชื่อ | ใช้ทำอะไร |
|---|---|
| `DATABASE_*` | ระบบส่งให้อัตโนมัติ |

## 7. สิ่งที่เปลี่ยนในการ deploy ครั้งนี้ (What changed)

- 

## 8. การทดสอบ (Tests)

- คำสั่งที่รันและผล (ตรงกับ test_report ที่แนบไป):

## 9. ความเสี่ยงและข้อจำกัดที่ผู้อนุมัติควรรู้ (Risks)

- 
