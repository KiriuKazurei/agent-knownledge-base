"""Generate a raster-only PDF for complex-layout OCR acceptance."""

from pathlib import Path

from PIL import Image, ImageDraw, ImageFont


ROOT = Path(__file__).resolve().parents[1]
OUTPUT = ROOT / "output" / "pdf" / "ocr-complex-layout-acceptance.pdf"
FONT = Path("C:/Windows/Fonts/msyh.ttc")
FONT_BOLD = Path("C:/Windows/Fonts/msyhbd.ttc")
PAGE_SIZE = (1654, 2339)


def font(size: int, bold: bool = False) -> ImageFont.FreeTypeFont:
    return ImageFont.truetype(str(FONT_BOLD if bold else FONT), size)


def page() -> tuple[Image.Image, ImageDraw.ImageDraw]:
    image = Image.new("RGB", PAGE_SIZE, "white")
    return image, ImageDraw.Draw(image)


def two_columns() -> Image.Image:
    image, draw = page()
    draw.text((90, 70), "复杂版式验收 - 双栏阅读顺序", font=font(62, True), fill="#111111")
    draw.line((827, 190, 827, 2160), fill="#bbbbbb", width=3)
    left = [
        "L1 左栏第一节：离线知识入口。",
        "L2 左栏第二节：保持来源定位。",
        "L3 左栏第三节：完成审核闭环。",
    ]
    right = [
        "R1 右栏第一节：禁止隐藏联网。",
        "R2 右栏第二节：错误必须可见。",
        "R3 右栏第三节：支持安全卸载。",
    ]
    for index, text in enumerate(left):
        draw.text((100, 280 + index * 360), text, font=font(42), fill="#111111")
        draw.multiline_text((100, 350 + index * 360), "本段正文用于验证同一栏内的\n连续阅读顺序。", font=font(32), fill="#333333", spacing=18)
    for index, text in enumerate(right):
        draw.text((880, 280 + index * 360), text, font=font(42), fill="#111111")
        draw.multiline_text((880, 350 + index * 360), "右栏内容应在左栏结束后\n再进入阅读序列。", font=font(32), fill="#333333", spacing=18)
    draw.text((90, 2220), "P1-END 双栏页结束", font=font(28), fill="#555555")
    return image


def table_page() -> Image.Image:
    image, draw = page()
    draw.text((90, 70), "复杂版式验收 - 表格与合并标题", font=font(62, True), fill="#111111")
    x = [100, 420, 850, 1210, 1550]
    y = [250, 360, 500, 640, 780, 920, 1060]
    draw.rectangle((x[0], y[0], x[-1], y[-1]), outline="#111111", width=4)
    draw.rectangle((x[0], y[0], x[-1], y[1]), fill="#dceeea", outline="#111111", width=4)
    draw.text((570, 275), "季度 OCR 验收统计", font=font(42, True), fill="#111111")
    for x_value in x[1:-1]:
        draw.line((x_value, y[1], x_value, y[-1]), fill="#111111", width=3)
    for y_value in y[1:-1]:
        draw.line((x[0], y_value, x[-1], y_value), fill="#111111", width=3)
    headers = ["编号", "类型", "页数", "状态"]
    rows = [
        ["T01", "扫描合同", "12", "通过"],
        ["T02", "双栏报告", "8", "复核"],
        ["T03", "旋转附件", "3", "失败"],
        ["T04", "低对比度", "5", "通过"],
    ]
    for column, value in enumerate(headers):
        draw.text((x[column] + 22, y[1] + 35), value, font=font(34, True), fill="#111111")
    for row_index, row in enumerate(rows):
        for column, value in enumerate(row):
            draw.text((x[column] + 22, y[row_index + 2] + 35), value, font=font(32), fill="#111111")
    draw.multiline_text(
        (100, 1200),
        "表格说明：T03 需要人工复核。\n注释 A：状态列不能与页数列串行。\n注释 B：每一行必须保留对应关系。",
        font=font(38),
        fill="#222222",
        spacing=28,
    )
    draw.text((90, 2220), "P2-END 表格页结束", font=font(28), fill="#555555")
    return image


def rotated_and_low_contrast() -> Image.Image:
    image, draw = page()
    draw.text((90, 70), "复杂版式验收 - 旋转与低对比度", font=font(62, True), fill="#111111")
    draw.text((100, 250), "C1 低对比度正文：灰色文字也应被识别。", font=font(42), fill="#aaaaaa")
    draw.text((100, 360), "C2 清晰对照正文：用于判断漏识别。", font=font(42), fill="#111111")
    rotated = Image.new("RGBA", (900, 180), (255, 255, 255, 0))
    rotated_draw = ImageDraw.Draw(rotated)
    rotated_draw.text((10, 25), "ROT90 旋转九十度文字", font=font(48, True), fill="#111111")
    rotated = rotated.rotate(90, expand=True, fillcolor="white")
    image.paste(rotated.convert("RGB"), (1180, 600))
    draw.rounded_rectangle((100, 700, 1050, 1350), radius=30, outline="#333333", width=4)
    draw.text((150, 760), "侧栏卡片", font=font(48, True), fill="#111111")
    draw.multiline_text((150, 860), "S1 重点提示\nS2 次要提示\nS3 操作建议", font=font(40), fill="#222222", spacing=55)
    draw.text((90, 2220), "P3-END 旋转页结束", font=font(28), fill="#555555")
    return image


def hierarchy_page() -> Image.Image:
    image, draw = page()
    draw.rectangle((0, 0, PAGE_SIZE[0], 190), fill="#193d59")
    draw.text((90, 58), "复杂版式验收 - 层级与脚注", font=font(62, True), fill="white")
    draw.text((100, 260), "H1 一级标题：转换流程", font=font(52, True), fill="#111111")
    draw.text((140, 390), "H1.1 二级标题：提取", font=font(43, True), fill="#1b5d76")
    draw.multiline_text((180, 480), "第一步：读取来源。\n第二步：识别页面。\n第三步：记录定位。[1]", font=font(37), fill="#222222", spacing=26)
    draw.text((140, 800), "H1.2 二级标题：转换", font=font(43, True), fill="#1b5d76")
    draw.multiline_text((180, 890), "第四步：形成 reference KAH。\n第五步：等待人工审核。", font=font(37), fill="#222222", spacing=26)
    draw.line((100, 2030, 1550, 2030), fill="#777777", width=2)
    draw.text((100, 2070), "[1] 页级定位必须可追溯。", font=font(28), fill="#555555")
    draw.text((90, 2220), "P4-END 文档结束", font=font(28), fill="#555555")
    return image


def main() -> None:
    OUTPUT.parent.mkdir(parents=True, exist_ok=True)
    pages = [two_columns(), table_page(), rotated_and_low_contrast(), hierarchy_page()]
    pages[0].save(OUTPUT, "PDF", resolution=200, save_all=True, append_images=pages[1:])
    print(OUTPUT)


if __name__ == "__main__":
    main()
