"""Package this review document for offline opening; no application build."""
from pathlib import Path

root = Path(__file__).resolve().parent
html = (root / 'index.html').read_text()
html = html.replace('<link rel="stylesheet" href="style.css">', '<style>\n' + (root / 'style.css').read_text() + '\n</style>')
html = html.replace('<script src="experience.js"></script>', '<script>\n' + (root / 'experience.js').read_text() + '\n</script>')
html = html.replace('<script src="app.js"></script>', '<script>\n' + (root / 'app.js').read_text() + '\n</script>')
(root / 'standalone.html').write_text(html)
print('Updated standalone.html')
