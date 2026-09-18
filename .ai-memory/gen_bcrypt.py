# -*- coding: utf-8 -*-
import bcrypt

pwd = bcrypt.hashpw(b"admin123", bcrypt.gensalt(10)).decode()
print(pwd)
