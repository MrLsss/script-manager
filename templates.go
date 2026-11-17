package main

// starterContent is the text a new script of a language begins with.
func starterContent(lang, name string) string {
	switch lang {
	case "python":
		return "#!/usr/bin/env python3\n\n\ndef main():\n    print(\"" + name + "\")\n\n\nif __name__ == \"__main__\":\n    main()\n"
	case "shell":
		return "#!/usr/bin/env bash\nset -euo pipefail\n\necho \"" + name + "\"\n"
	case "javascript":
		return "#!/usr/bin/env node\n\nconsole.log(\"" + name + "\");\n"
	case "typescript":
		return "console.log(\"" + name + "\");\n"
	case "powershell":
		return "Write-Output \"" + name + "\"\n"
	case "ruby":
		return "#!/usr/bin/env ruby\n\nputs \"" + name + "\"\n"
	case "perl":
		return "#!/usr/bin/env perl\nuse strict;\nuse warnings;\n\nprint \"" + name + "\\n\";\n"
	case "lua":
		return "print(\"" + name + "\")\n"
	case "php":
		return "<?php\n\necho \"" + name + "\\n\";\n"
	case "r":
		return "cat(\"" + name + "\\n\")\n"
	case "go":
		return "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"" + name + "\")\n}\n"
	case "swift":
		return "import Foundation\n\nprint(\"" + name + "\")\n"
	case "dart":
		return "void main() {\n  print('" + name + "');\n}\n"
	}
	return ""
}
