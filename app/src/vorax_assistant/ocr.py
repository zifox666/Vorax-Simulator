from dataclasses import asdict, dataclass
from pathlib import Path


@dataclass(frozen=True)
class Line:
    text: str
    box: list[list[float]]
    confidence: float

    @property
    def x(self) -> float:
        return sum(p[0] for p in self.box) / len(self.box)

    @property
    def y(self) -> float:
        return sum(p[1] for p in self.box) / len(self.box)

    @property
    def height(self) -> float:
        return max(p[1] for p in self.box) - min(p[1] for p in self.box)


@dataclass(frozen=True)
class Frame:
    width: int
    height: int
    lines: list[Line]

    def to_dict(self) -> dict:
        return asdict(self)

    @classmethod
    def from_dict(cls, data: dict) -> "Frame":
        if data["width"] <= 0 or data["height"] <= 0:
            raise ValueError("OCR 数据的画面尺寸无效")
        lines = [Line(**line) for line in data["lines"]]
        if any(len(line.box) != 4 or any(len(p) != 2 for p in line.box) for line in lines):
            raise ValueError("OCR 数据须包含文字四点坐标")
        return cls(data["width"], data["height"], lines)


class Reader:
    def __init__(self, model_dir: Path):
        from rapidocr import EngineType, OCRVersion, RapidOCR

        files = [model_dir / name for name in (
            "ch_PP-OCRv6_det_infer.onnx", "ch_PP-OCRv6_rec_infer.onnx", "ch_ppocrv6_dict.txt",
        )]
        for file in files:
            if not file.is_file():
                raise FileNotFoundError(f"缺少本地模型：{file}")
        self.engine = RapidOCR(params={
            "Global.use_cls": False,
            "Global.log_level": "error",
            "Global.max_side_len": 3200,
            "Det.engine_type": EngineType.ONNXRUNTIME,
            "Rec.engine_type": EngineType.ONNXRUNTIME,
            "Det.ocr_version": OCRVersion.PPOCRV6,
            "Rec.ocr_version": OCRVersion.PPOCRV6,
            "Det.model_path": str(files[0]),
            "Rec.model_path": str(files[1]),
            "Rec.rec_keys_path": str(files[2]),
            "Det.limit_side_len": 1600,
            "Det.limit_type": "max",
            "EngineConfig.onnxruntime.intra_op_num_threads": 4,
            "EngineConfig.onnxruntime.inter_op_num_threads": 1,
            "EngineConfig.onnxruntime.use_cuda": False,
            "EngineConfig.onnxruntime.use_dml": False,
        })

    def read(self, picture) -> Frame:
        result = self.engine(picture, use_cls=False)
        lines = []
        if result.boxes is not None:
            lines = [Line(text, box.tolist(), float(score))
                     for box, text, score in zip(result.boxes, result.txts, result.scores)]
        return Frame(picture.width, picture.height, lines)
