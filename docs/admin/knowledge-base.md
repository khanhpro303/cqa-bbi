# Tài liệu chatbot (knowledge base)

Trang **Tài liệu chatbot** (`/<tenant>/knowledge`, quyền `settings`) là nơi quản lý
tài liệu nghiệp vụ mà bot Zalo dùng để trả lời: chính sách bán hàng, bảo hành,
đổi trả, giao hàng, hướng dẫn sử dụng, quy trình nội bộ...

Trước đây tài liệu được nạp bằng các flow ingestion trên Langflow vào Astra DB
`bbi` (keyspace và collection `bbi_rag`), và flow bot đọc qua node AstraDB
"KnowledgeDB". Hai flow ingestion đó đã bị xoá ngày 18/11/2026 nên không còn chỗ
tải tài liệu mới. Trang này thay thế chúng.

## Bot đọc tài liệu thế nào

Theo hướng MCP-first: CQA thêm công cụ `search_knowledge` vào MCP server ERP
(`/mcp/erp/<tenant-id>`), cùng chỗ với `search_products`, `lookup_order`... Agent
trong Langflow tự quyết định khi nào gọi.

- Đầu vào: `query` (bắt buộc), `limit` (1-10, mặc định 5).
- Đầu ra: `passages[]` gồm `source` (tên tài liệu), `content`, `score`; nếu không
  có đoạn nào khớp thì `note = "no_matching_documents"`.
- Bot khách (agent token public) chỉ thấy tài liệu **Khách và nhân viên**. Bot
  nội bộ (token private) thấy cả tài liệu **Chỉ nhân viên**.
- Mô tả công cụ (đoạn văn bản agent đọc để biết khi nào tra) là prompt
  **"Tra tài liệu nghiệp vụ (search_knowledge)"** trong AI Engine → Prompt cho
  các tác vụ AI, sửa được, không hard-code.

Sau khi deploy, mở node **MCP Tools** trong cả hai flow v2 trên Langflow và làm
mới danh sách tool để agent thấy `search_knowledge`. Mỗi lần sửa mô tả công cụ
trong AI Engine cũng cần làm mới như vậy.

## Lưu trữ

- MySQL là nguồn gốc: `knowledge_documents` (một dòng một tài liệu) và
  `knowledge_chunks` (các đoạn ~1.200 ký tự, gối 200 ký tự).
- Vector: một embedding cho mỗi đoạn, trên cùng kho vector và model embedding
  với trí nhớ khách (`CHAT_MEMORY_VECTOR_STORE`, `CHAT_MEMORY_EMBEDDING_*`),
  trong collection riêng `KNOWLEDGE_ASTRA_COLLECTION` (mặc định `cqa_knowledge`)
  hoặc index Vectorize `KNOWLEDGE_VECTORIZE_INDEX` (mặc định `cqa-knowledge`,
  cần metadata index `tenant_id` và `audience`).
- Kho vector chỉ chứa tài liệu đang bật và chưa lưu trữ, dựng lại được từ MySQL
  bất cứ lúc nào (nút "Lập chỉ mục lại"). Khi tìm, MySQL quyết định đoạn nào
  còn hiệu lực, nên vector cũ sót lại không lọt ra.
- Không có kho vector: bot vẫn tra được bằng tìm theo từ khoá trong MySQL (trang
  hiện cảnh báo).

## Thao tác trên trang

- **Tải tài liệu**: PDF (có lớp chữ, PDF scan cần OCR trước), DOCX, XLSX, TXT,
  MD, CSV, HTML; tối đa 20 MB mỗi tệp. Chọn "Ai được dùng".
- **Nhập văn bản**: dán nội dung trực tiếp.
- **Thử hỏi như bot**: chạy đúng phép tìm mà bot chạy, với tư cách khách hoặc
  nhân viên.
- **Bật/tắt**: tắt thì bot không dùng nữa (xoá vector), bật lại thì lập chỉ mục lại.
- **Lưu trữ / Khôi phục**: xoá mềm. **Xoá vĩnh viễn** chỉ cho owner/admin và
  chỉ với tài liệu đã lưu trữ.
- **Nhập từ Astra cũ** (owner/admin): chép các đoạn trong collection Astra mà
  flow Langflow cũ đã nạp, gom theo tệp gốc (`metadata.file_path` / `source`),
  mỗi tệp thành một tài liệu, rồi nhúng lại bằng model của CQA. Token chỉ dùng
  cho lần nhập đó, không lưu. Endpoint phải là `https://*.apps.astra.datastax.com`.

## Chuyển từ node KnowledgeDB sang search_knowledge

1. Deploy, mở trang Tài liệu chatbot, dùng "Nhập từ Astra cũ" với database
   `bbi` (keyspace/collection `bbi_rag`), hoặc tải lại các tệp gốc.
2. Thử vài câu hỏi trong ô "Thử hỏi như bot".
3. Trên Langflow, làm mới tool của node MCP Tools ở flow Int_v2 và Ext_v2.
4. Khi bot đã trả lời đúng qua `search_knowledge`, có thể bỏ node KnowledgeDB
   (nếu flow còn) để bot chỉ có một nguồn tài liệu, quản lý ở CQA.
