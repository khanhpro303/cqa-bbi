from lfx.custom.custom_component.component import Component
from lfx.inputs.inputs import MessageTextInput
from lfx.io import SecretStrInput, Output
from lfx.schema.message import Message
import httpx
import json

class ERPGatewayCaller(Component):
    display_name = "ERP Gateway Caller"
    description = (
        "Truy vấn dữ liệu ERP (sản phẩm, tồn kho, đơn hàng, công nợ) từ hệ thống ERP Gateway."
    )

    inputs = [
        MessageTextInput(
            name="gateway_endpoint",
            display_name="Gateway Endpoint",
            info="Đường dẫn API ERP Query (ví dụ: https://cqa.kariendoan.xyz/api/tenants/{tenant_id}/erp/query)",
            required=True,
            tool_mode=False
        ),
        SecretStrInput(
            name="secure_token",
            display_name="Secure Token",
            info="Agent Secure Token dùng để xác thực (X-Agent-Token)",
        ),
        MessageTextInput(
            name="resource",
            display_name="Resource",
            info=(
                "Loại tài nguyên cần truy vấn: 'products' (tìm dòng SP/giá range), "
                "'product_variants' (giá/tồn của variant cụ thể theo màu/size), "
                "'inventory', 'orders', 'debt'."
            ),
            required=True,
            tool_mode=True
        ),
        MessageTextInput(
            name="search",
            display_name="Search",
            info=(
                "Từ khóa tìm kiếm ĐÃ LÀM SẠCH (tên sản phẩm, mã SKU, mã đơn hàng, "
                "mã/tên khách...). Với resource='orders'/'debt' bạn TỰ nhận diện ý "
                "định và CHỈ truyền định danh cụ thể: có khách → mã/tên khách trần "
                "(vd 'S001', 'Huy'); chưa nêu khách → đúng cụm generic 'đơn hàng' / "
                "'công nợ'. TUYỆT ĐỐI không truyền cả câu chat ('tôi cần hỏi đơn "
                "hàng của khách…') — chữ thừa sẽ LIKE-trùng nhiều khách và làm "
                "backend liệt kê nhầm danh sách thay vì hỏi 'khách nào?'."
            ),
            required=False,
            tool_mode=True
        ),
        MessageTextInput(
            name="parent_code",
            display_name="Parent Code",
            info="Mã cha (BẮT BUỘC khi resource=product_variants; dùng để khoá truy vấn vào dòng SP cụ thể)",
            required=False,
            tool_mode=True
        ),
        MessageTextInput(
            name="color",
            display_name="Color",
            info="Màu sắc/thuộc tính 1 (vd: 'đen bóng', 'xanh navy'). Dùng với resource=product_variants, HOẶC kèm resource=products+intent=price để backend pivot ra đúng đơn giá biến thể. Bỏ trống nếu user không nêu.",
            required=False,
            tool_mode=True
        ),
        MessageTextInput(
            name="size",
            display_name="Size",
            info="Kích thước/thuộc tính 2 (vd: 'L', 'XL'). Dùng với resource=product_variants, HOẶC kèm resource=products+intent=price để backend pivot ra đúng đơn giá biến thể. Bỏ trống nếu user không nêu.",
            required=False,
            tool_mode=True
        ),
        MessageTextInput(
            name="brand",
            display_name="Brand",
            info=(
                "Thương hiệu user TỰ nêu để lọc biến thể (tuỳ chọn, dùng với "
                "resource=product_variants). ĐÂY KHÔNG phải boundary phân quyền: "
                "việc giới hạn nhãn hiệu theo nhóm agent được enforce ở backend "
                "(permission token), agent không cần và không nên tự suy ra."
            ),
            required=False,
            tool_mode=True
        ),
        MessageTextInput(
            name="exact_web_name",
            display_name="Exact Web Name",
            info=(
                "Đặt 'true' khi 'search' là MỘT tên web khách đã chọn từ danh sách trước "
                "(vd: 'LS2 FF901'). Backend khớp CHÍNH XÁC tên web và KHÔNG đẩy lại danh sách — "
                "dùng để cắt vòng lặp khi tên ngắn là tiền tố của tên dài "
                "('LS2 FF901' vs 'LS2 FF901 Carbon'). Bỏ trống cho tìm kiếm thường."
            ),
            required=False,
            tool_mode=True
        ),
        MessageTextInput(
            name="intent",
            display_name="Intent",
            info=(
                "Ý định GIÁ vs TỒN của câu hỏi sản phẩm gốc: đặt 'price' khi khách hỏi GIÁ "
                "('giá bao nhiêu', 'bán bao nhiêu', 'đơn giá'); để trống hoặc 'stock' khi hỏi "
                "TỒN KHO hoặc mơ hồ. Backend nướng giá trị này vào nút chọn dòng — khi khách bấm "
                "số để chọn từ danh sách, bot trả ĐÚNG giá (intent=price) thay vì hỏi tồn. "
                "QUAN TRỌNG cho câu hỏi GIÁ của 1 biến thể CỤ THỂ (có màu/size): truyền "
                "intent='price' KÈM color/size ngay trên call resource='products' — backend sẽ "
                "tự pivot sang variant và trả đúng đơn giá SKU (không trả price_range). "
                "Mặc định 'stock' nếu bỏ trống (an toàn: không bao giờ hiện giá sai)."
            ),
            required=False,
            tool_mode=True
        ),
        MessageTextInput(
            name="zalo_user_id",
            display_name="Zalo User ID",
            info="Zalo user ID của khách hàng",
            required=False,
            tool_mode=False
        ),
        MessageTextInput(
            name="permission_token",
            display_name="Permission Token",
            info="Token phân quyền được cấp",
            required=False,
            tool_mode=False
        )
    ]

    outputs = [
        Output(display_name="Output Message", name="output_message", method="call_api")
    ]

    @staticmethod
    def _coerce_text(value) -> str:
        if value is None:
            return ""
        if hasattr(value, "text"):
            value = value.text
        return str(value).strip()

    @staticmethod
    def _format_price(value) -> str:
        try:
            num = float(value)
        except (TypeError, ValueError):
            return "Liên hệ"
        if num <= 0:
            return "Liên hệ"
        return f"{int(round(num)):,}đ".replace(",", ".")

    def _format_variant_response(self, data: dict, parent_code: str, color: str, size: str) -> str:
        items = data.get("data") or []
        if items:
            lines = [f"Tìm thấy {len(items)} variant khớp parent={parent_code} color='{color}' size='{size}':"]
            for v in items:
                lines.append(
                    f"- ma={v.get('ma','')} | màu={v.get('color','')} | size={v.get('size','')} "
                    f"| giá={self._format_price(v.get('price'))} | dvt={v.get('dvt','')} "
                    f"| brand={v.get('nhan_hieu_name','')}"
                )
            lines.append(
                "Agent: ĐÂY LÀ BƯỚC GIỮA, chưa chắc là câu trả lời cuối. Đọc `ma` của data[0]. "
                "Nếu intent là TỒN/CÒN HÀNG/SỐ LƯỢNG (kể cả khi khách đã bấm 'xem theo mã SKU "
                "cụ thể' rồi mới nhập màu/size) → BẮT BUỘC gọi tiếp resource='inventory', "
                "search=<ma> để đọc ton_kho/TON_KHO; TUYỆT ĐỐI KHÔNG dừng ở giá vì response "
                "product_variants KHÔNG chứa tồn kho. Chỉ khi khách hỏi GIÁ rõ ràng mới chốt "
                "`price` của variant khớp và dừng tại đây."
            )
            return "\n".join(lines)

        colors = data.get("available_colors") or []
        sizes = data.get("available_sizes") or []
        hint_lines = [
            f"Không tìm thấy variant cho parent={parent_code} với color='{color}' size='{size}'."
        ]
        if colors:
            hint_lines.append(f"Các màu có sẵn: {', '.join(colors)}")
        if sizes:
            hint_lines.append(f"Các size có sẵn: {', '.join(sizes)}")
        hint_lines.append(
            "Agent: hỏi lại user theo các tuỳ chọn có sẵn ở trên, KHÔNG bịa giá."
        )
        return "\n".join(hint_lines)

    async def call_api(self) -> Message:
        gateway_url = self.gateway_endpoint
        token = self.secure_token

        if not gateway_url or not token:
            return Message(text="Lỗi cấu hình: Thiếu Gateway Endpoint hoặc Secure Token ở cài đặt node.")

        res = self._coerce_text(getattr(self, "resource", "")) or "inventory"
        search_query = self._coerce_text(getattr(self, "search", ""))
        p_code = self._coerce_text(getattr(self, "parent_code", ""))
        color = self._coerce_text(getattr(self, "color", ""))
        size = self._coerce_text(getattr(self, "size", ""))
        brand = self._coerce_text(getattr(self, "brand", ""))
        exact_web_raw = self._coerce_text(getattr(self, "exact_web_name", ""))
        exact_web = exact_web_raw.lower() in ("true", "1", "yes", "có", "co")
        intent = self._coerce_text(getattr(self, "intent", "")).strip().lower() or "stock"
        zalo_id = self._coerce_text(getattr(self, "zalo_user_id", ""))
        perm_token = self._coerce_text(getattr(self, "permission_token", ""))

        if res == "product_variants" and not p_code:
            return Message(text=(
                "Lỗi: Resource 'product_variants' yêu cầu parent_code (mã cha). "
                "Agent: hãy gọi lại resource='products' với search=<từ khoá> trước để xác định parent_code, "
                "sau đó mới gọi product_variants với màu/size cụ thể."
            ))

        payload = {
            "resource": res,
            "search": search_query,
            "parent_code": p_code,
            "color": color,
            "size": size,
            "brand": brand,
            "exact_web_name": exact_web,
            "intent": intent,
            "zalo_user_id": zalo_id,
            "limit": 10,
        }

        headers = {
            "Content-Type": "application/json",
            "X-Agent-Token": str(token).strip(),
        }
        if perm_token:
            headers["X-Permission-Token"] = perm_token

        async with httpx.AsyncClient(timeout=30.0) as client:
            try:
                resp = await client.post(gateway_url, json=payload, headers=headers)
                status = resp.status_code
                data = resp.json()
            except Exception as e:
                return Message(text=f"Lỗi kết nối đến gateway: {str(e)}")

        if status != 200 or data.get("status") != "success":
            err_msg = data.get("message", "Lỗi phản hồi từ ERP Gateway.")
            return Message(text=f"Không thể lấy dữ liệu ERP: {err_msg}")

        # Render variant answers for BOTH an explicit product_variants call and a
        # backend price-pivot: when the customer asked the PRICE of a concrete
        # color/size, the backend re-resolves the exact SKU under resource=products
        # and tags the response `pivoted_from=products` (it never hands back a
        # family price_range for a named variant). Format it as a variant either
        # way so the bot answers the exact price, or surfaces available_* on a miss.
        if res == "product_variants" or data.get("pivoted_from") == "products":
            resp_parent = self._coerce_text(data.get("parent_code")) or p_code
            return Message(text=self._format_variant_response(data, resp_parent, color, size))

        if (
            data.get("is_orders_prompt") or data.get("is_debt_prompt")
            or data.get("is_inventory_rich")
            or data.get("is_product_rich")
            or data.get("message") == "zalo_rich_message_sent_directly"
        ):
            items_local = data.get("data") or []
            hint = ""
            if items_local and isinstance(items_local[0], dict) and items_local[0].get("message"):
                hint = items_local[0]["message"]
            if data.get("is_product_rich") and data.get("numbered_list"):
                return Message(text=(
                    f"[RICH_MESSAGE_SENT] {hint}\n\n"
                    f"Danh sách tương ứng:\n{data.get('numbered_list')}\n\n"
                    "Agent: Trong câu trả lời cuối cùng, BẮT BUỘC liệt kê danh sách trên "
                    "theo định dạng '1. SP… / 2. SP… / 3. SP…' để user có thể trả lời bằng "
                    "số (1/2/3) hoặc mã SP. KHÔNG nói 'đã gửi danh sách qua Zalo' nếu danh "
                    "sách đã được liệt kê. KHÔNG tự ý hỏi màu/size — sau khi user chọn số, "
                    "follow Disambiguation Follow-up Rules trong system prompt (STOCK → gọi "
                    "inventory với search=<web_name>; PRICE-only → gọi products với "
                    "exact_web_name=true)."
                ))
            return Message(text=f"[RICH_MESSAGE_SENT] {hint}".rstrip())

        # Orders date-window answers carry orders_summary (pre-aggregated count /
        # value / per-status). Surface it directly — including the 0-order case —
        # so the LLM reports the summary instead of falling into the generic
        # count==0 "không tìm thấy thông tin" branch below, which made a valid
        # "no orders in this window" answer look like a lookup failure. Single
        # order lookups (order_code, count:1) have NO orders_summary, so they fall
        # through to the normal listing untouched.
        if res == "orders" and isinstance(data.get("orders_summary"), dict):
            summary = data["orders_summary"]
            frm = self._coerce_text(data.get("from"))
            to = self._coerce_text(data.get("to"))
            period = f"từ {frm} đến {to}" if frm and to else "trong khoảng đã chọn"
            codes = data.get("customer_codes") or []
            who = f" của khách {', '.join(str(c) for c in codes)}" if codes else ""
            total_orders = summary.get("total_orders", 0) or 0
            if not total_orders:
                return Message(text=f"Không có đơn hàng nào{who} {period}.")
            total_value = self._format_price(summary.get("total_value"))
            lines = [f"Đơn hàng{who} {period}: tổng {total_orders} đơn, trị giá {total_value}."]
            for bucket in summary.get("by_status") or []:
                name = self._coerce_text(bucket.get("status_name")) or "Không xác định"
                lines.append(f"- {name}: {bucket.get('count', 0)} đơn")
            lines.append(
                "Agent: trả lời ngắn gọn dựa trên các con số trên (khoảng thời gian, "
                "tổng số đơn + trị giá, rồi breakdown theo trạng thái). KHÔNG tự đếm lại."
            )
            return Message(text="\n".join(lines))

        if data.get("is_ma_cha"):
            return Message(text=data.get("message", "Dòng sản phẩm này có nhiều phân loại kích thước/màu sắc."))

        items = data.get("data", [])
        source = data.get("source", "unknown")
        count = data.get("count", 0)
        # parent_codes is a TOP-LEVEL field on the exact-web products response
        # (source=astradb_cache_exact_web) — the line's ma_cha lives here, NOT
        # inside data[] the way the web-groups disambiguation payload carries it.
        # Without echoing it, the STOCK-pick continuation ("KHÓA ĐÚNG DÒNG ĐÃ
        # CHỌN") has no parent_code to copy into product_variants, so the variant
        # lookup dead-ends at count=0 and the bot wrongly says it can't lock the SKU.
        parent_codes = data.get("parent_codes") or []

        if count == 0:
            return Message(text=f"Không tìm thấy thông tin '{res}' cho từ khóa '{search_query}'.")

        lines = [f"Tìm thấy {count} kết quả {res} (Nguồn: {source}):"]
        for item in items[:10]:
            lines.append(json.dumps(item, ensure_ascii=False))
        if parent_codes:
            codes_str = ", ".join(f'"{str(pc)}"' for pc in parent_codes)
            lines.append(f"parent_codes: [{codes_str}]")
            lines.append(
                "Agent: để tra biến thể (màu/size) hoặc tồn theo SKU của dòng này, "
                "COPY parent_codes[0] ở trên VERBATIM sang resource='product_variants' "
                "(parent_code=<parent_codes[0]>, color/size theo khách). TUYỆT ĐỐI "
                "không bịa parent_code từ tên web."
            )
        return Message(text="\n".join(lines))